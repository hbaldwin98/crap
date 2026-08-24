package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeCodeGraphBuildsCrossLanguageContainment(t *testing.T) {
	root := t.TempDir()
	writeGraphSource(t, root, "sample.go", "package sample\ntype Work struct{}\nfunc Top() {\n\ttype Local struct{}\n\tf := func() {}\n\tf()\n}\nfunc (Work) Run() {}\n")
	writeGraphSource(t, root, "sample.cs", "namespace Demo; class Work { void Run() { void Local() {} } }")
	writeGraphSource(t, root, "sample.ts", "namespace Demo { export class Work { run() { const local = () => 1 } } }")

	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	report, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"sample.ts", "sample.go", "sample.cs"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReportType != "code-graph" || report.SchemaVersion != "1" {
		t.Fatalf("report identity = %s v%s", report.ReportType, report.SchemaVersion)
	}
	if report.Summary.Files != 3 || report.Summary.Types < 4 || report.Summary.Callables < 7 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if report.Summary.Nodes != len(report.Nodes) || report.Summary.Edges != len(report.Edges) || report.Summary.Nodes != report.Summary.Edges+report.Summary.Files {
		t.Fatalf("inconsistent forest summary = %#v", report.Summary)
	}
	nodes := make(map[string]CodeGraphNode, len(report.Nodes))
	for _, node := range report.Nodes {
		nodes[node.ID] = node
	}
	foundDeclares, foundNestedCallable, foundLocalType := false, false, false
	for _, edge := range report.Edges {
		from, to := nodes[edge.From], nodes[edge.To]
		if edge.Type == "declares" && from.Kind == "type" && to.Kind == "callable" {
			foundDeclares = true
		}
		if edge.Type == "contains" && from.Kind == "callable" && to.Kind == "callable" {
			foundNestedCallable = true
		}
		if edge.Type == "contains" && from.Kind == "callable" && to.Kind == "type" {
			foundLocalType = true
		}
	}
	if !foundDeclares || !foundNestedCallable || !foundLocalType {
		t.Fatalf("relationships: declares=%t nested-callable=%t local-type=%t", foundDeclares, foundNestedCallable, foundLocalType)
	}

	first, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	again, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"sample.ts", "sample.go", "sample.cs"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(again)
	if string(first) != string(second) {
		t.Fatal("code graph output changed with source argument ordering")
	}
}

func TestAnalyzeCodeGraphUsesNearestLexicalDeclarationParent(t *testing.T) {
	root := t.TempDir()
	writeGraphSource(t, root, "nested.ts", "function outer() { class Inner { method() { class Local {} } } }\n")
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"nested.ts"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]CodeGraphNode, len(report.Nodes))
	ids := make(map[string]string)
	for _, node := range report.Nodes {
		nodes[node.ID] = node
		ids[node.Name] = node.ID
	}
	parents := make(map[string]CodeGraphEdge)
	for _, edge := range report.Edges {
		parents[edge.To] = edge
	}
	outer, inner, method, local := findGraphNodeID(ids, "outer"), findGraphNodeID(ids, "Inner"), findGraphNodeID(ids, "method"), findGraphNodeID(ids, "Local")
	if outer == "" || inner == "" || method == "" || local == "" {
		t.Fatalf("nodes = %#v", nodes)
	}
	if edge := parents[inner]; edge.From != outer || edge.Type != "contains" {
		t.Fatalf("Inner parent = %#v", edge)
	}
	if edge := parents[method]; edge.From != inner || edge.Type != "declares" {
		t.Fatalf("method parent = %#v", edge)
	}
	if edge := parents[local]; edge.From != method || edge.Type != "contains" {
		t.Fatalf("Local parent = %#v", edge)
	}
}

func TestAnalyzeCodeGraphBuildsGoModuleDependencies(t *testing.T) {
	root := t.TempDir()
	writeGraphSource(t, root, "go.mod", "module example.test/app\n")
	writeGraphSource(t, root, "a/a.go", "package a\nfunc Work() {}\n")
	writeGraphSource(t, root, "b/b.go", "package b\nimport \"example.test/app/a\"\nfunc Run() { a.Work() }\n")
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	report, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"a", "b"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Modules != 2 || report.Summary.ImportsEdges != 1 || report.Summary.ResolvedReferences != 1 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	modules := graphModulesByName(report.Nodes)
	if modules["example.test/app/a"] == "" || modules["example.test/app/b"] == "" {
		t.Fatalf("modules = %#v", modules)
	}
	if len(report.ResolutionInputs) != 1 || report.ResolutionInputs[0].Path != "go.mod" {
		t.Fatalf("resolution inputs = %#v", report.ResolutionInputs)
	}
	if edge := findGraphEdge(report.Edges, "imports"); edge.From != modules["example.test/app/b"] || edge.To != modules["example.test/app/a"] || edge.Occurrences != 1 {
		t.Fatalf("import edge = %#v", edge)
	}
}

func TestAnalyzeCodeGraphResolvesTypeScriptAndCSharpModulesConservatively(t *testing.T) {
	root := t.TempDir()
	writeGraphSource(t, root, "web/a.ts", "export function work() {}\n")
	writeGraphSource(t, root, "web/b.ts", "import { work } from './a'\nwork()\n")
	writeGraphSource(t, root, "one.cs", "namespace Demo.One { class Work {} }\n")
	writeGraphSource(t, root, "two.cs", "using Demo.One; namespace Demo.Two { class Run {} }\n")
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	report, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"web", "one.cs", "two.cs"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	modules := graphModulesByName(report.Nodes)
	if modules["web/a.ts"] == "" || modules["web/b.ts"] == "" || modules["Demo.One"] == "" || modules["Demo.Two"] == "" {
		t.Fatalf("modules = %#v", modules)
	}
	resolved := 0
	for _, reference := range report.References {
		if reference.Resolution == "resolved" {
			resolved++
		}
	}
	if resolved != 2 || report.Summary.ImportsEdges != 2 {
		t.Fatalf("resolved=%d summary=%#v references=%#v", resolved, report.Summary, report.References)
	}
}

func TestCodeGraphRejectsContainmentBeyondPublishedDepth(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{{ID: "file", Kind: "file"}}, Edges: []CodeGraphEdge{}}
	parent := "file"
	for index := 1; index <= MaximumCodeGraphDepth; index++ {
		id := fmt.Sprintf("node-%d", index)
		graph.Nodes = append(graph.Nodes, CodeGraphNode{ID: id, Kind: "callable"})
		graph.Edges = append(graph.Edges, CodeGraphEdge{ID: fmt.Sprintf("edge-%d", index), Type: "contains", From: parent, To: id})
		parent = id
	}
	if err := validateAndSummarizeCodeGraph(context.Background(), &graph); err == nil {
		t.Fatal("over-depth graph was accepted")
	}
}

func TestCodeGraphValidationRejectsMalformedNodesAndEdges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CodeGraphReport)
	}{
		{"duplicate node", func(graph *CodeGraphReport) { graph.Nodes = append(graph.Nodes, graph.Nodes[0]) }},
		{"module metadata", func(graph *CodeGraphReport) { graph.Nodes[1].Module = nil }},
		{"unsupported node", func(graph *CodeGraphReport) { graph.Nodes[2].Kind = "field" }},
		{"missing endpoint", func(graph *CodeGraphReport) { graph.Edges[0].To = "missing" }},
		{"duplicate edge", func(graph *CodeGraphReport) { graph.Edges = append(graph.Edges, graph.Edges[0]) }},
		{"invalid membership", func(graph *CodeGraphReport) { graph.Edges[2].From = "type" }},
		{"invalid dependency", func(graph *CodeGraphReport) { graph.Edges[3].From = "file" }},
		{"unsupported edge", func(graph *CodeGraphReport) { graph.Edges[0].Type = "calls" }},
		{"self edge", func(graph *CodeGraphReport) { graph.Edges[0].To = graph.Edges[0].From }},
		{"invalid declaration", func(graph *CodeGraphReport) { graph.Edges[1].From = "file" }},
		{"invalid containment", func(graph *CodeGraphReport) { graph.Edges[0].From, graph.Edges[0].To = "type", "callable" }},
		{"multiple parents", func(graph *CodeGraphReport) {
			graph.Edges = append(graph.Edges, CodeGraphEdge{ID: "extra", Type: "contains", From: "file", To: "callable"})
		}},
		{"file parent", func(graph *CodeGraphReport) {
			graph.Edges = append(graph.Edges, CodeGraphEdge{ID: "extra", Type: "contains", From: "callable", To: "file"})
		}},
		{"missing parent", func(graph *CodeGraphReport) { graph.Edges = graph.Edges[1:] }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			graph := validCodeGraphForValidation()
			test.mutate(&graph)
			if err := validateAndSummarizeCodeGraph(context.Background(), &graph); err == nil {
				t.Fatal("malformed graph was accepted")
			}
		})
	}
}

func TestCodeGraphReferenceValidationCoversEveryResolution(t *testing.T) {
	valid := []CodeGraphReference{
		{ID: "resolved", SourceFile: "file", SourceModule: "module-a", Resolution: "resolved", Target: "module-b"},
		{ID: "unresolved", SourceFile: "file", SourceModule: "module-a", Resolution: "unresolved", Reason: "outside-selected-sources"},
		{ID: "ambiguous", SourceFile: "file", SourceModule: "module-a", Resolution: "ambiguous", Candidates: []string{"module-b", "module-c"}, Reason: "multiple-candidates"},
	}
	edges := []CodeGraphEdge{{ID: "dependency", Type: "imports", From: "module-a", To: "module-b", References: []string{"resolved"}}}
	nodes := validCodeGraphNodesForValidation()
	graph := CodeGraphReport{}
	if err := validateCodeGraphReferences(context.Background(), &graph, nodes, edges, valid); err != nil {
		t.Fatal(err)
	}
	if graph.Summary.ResolvedReferences != 1 || graph.Summary.UnresolvedReferences != 1 || graph.Summary.AmbiguousReferences != 1 {
		t.Fatalf("summary = %#v", graph.Summary)
	}

	cases := []struct {
		name       string
		references []CodeGraphReference
		edges      []CodeGraphEdge
	}{
		{name: "duplicate", references: append(append([]CodeGraphReference{}, valid...), valid[0])},
		{name: "invalid source", references: []CodeGraphReference{{ID: "bad", SourceFile: "module-a", SourceModule: "module-a", Resolution: "unresolved", Reason: "missing"}}},
		{name: "invalid candidate", references: []CodeGraphReference{{ID: "bad", SourceFile: "file", SourceModule: "module-a", Resolution: "ambiguous", Candidates: []string{"file", "module-b"}, Reason: "multiple-candidates"}}},
		{name: "invalid resolved", references: []CodeGraphReference{{ID: "bad", SourceFile: "file", SourceModule: "module-a", Resolution: "resolved", Target: "file"}}},
		{name: "invalid unresolved", references: []CodeGraphReference{{ID: "bad", SourceFile: "file", SourceModule: "module-a", Resolution: "unresolved"}}},
		{name: "invalid ambiguous", references: []CodeGraphReference{{ID: "bad", SourceFile: "file", SourceModule: "module-a", Resolution: "ambiguous", Candidates: []string{"module-b"}, Reason: "multiple-candidates"}}},
		{name: "unsupported resolution", references: []CodeGraphReference{{ID: "bad", SourceFile: "file", SourceModule: "module-a", Resolution: "external"}}},
		{"invalid edge evidence", valid, []CodeGraphEdge{{ID: "dependency", Type: "imports", From: "module-a", To: "module-c", References: []string{"resolved"}}}},
		{name: "missing dependency edge", references: valid},
		{"duplicate dependency edge", valid, []CodeGraphEdge{
			{ID: "one", Type: "imports", From: "module-a", To: "module-b", References: []string{"resolved"}},
			{ID: "two", Type: "imports", From: "module-a", To: "module-b", References: []string{"resolved"}},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCodeGraphReferences(context.Background(), &CodeGraphReport{}, nodes, test.edges, test.references); err == nil {
				t.Fatal("invalid reference evidence was accepted")
			}
		})
	}
}

func validCodeGraphForValidation() CodeGraphReport {
	return CodeGraphReport{
		Nodes: []CodeGraphNode{
			{ID: "file", Kind: "file"},
			{ID: "module-a", Kind: "module", Module: &CodeGraphModuleIdentity{}, ModuleMetrics: &CodeGraphModuleMetrics{}},
			{ID: "type", Kind: "type"},
			{ID: "callable", Kind: "callable", Metrics: &CodeGraphMetrics{AboveThreshold: true}},
			{ID: "module-b", Kind: "module", Module: &CodeGraphModuleIdentity{}, ModuleMetrics: &CodeGraphModuleMetrics{}},
		},
		Edges: []CodeGraphEdge{
			{ID: "contains", Type: "contains", From: "file", To: "type"},
			{ID: "declares", Type: "declares", From: "type", To: "callable"},
			{ID: "membership", Type: "member-of", From: "file", To: "module-a"},
			{ID: "dependency", Type: "imports", From: "module-a", To: "module-b"},
		},
	}
}

func validCodeGraphNodesForValidation() map[string]CodeGraphNode {
	return map[string]CodeGraphNode{
		"file":     {ID: "file", Kind: "file"},
		"module-a": {ID: "module-a", Kind: "module"},
		"module-b": {ID: "module-b", Kind: "module"},
		"module-c": {ID: "module-c", Kind: "module"},
	}
}

func findGraphNodeID(nodes map[string]string, fragment string) string {
	for name, id := range nodes {
		if name == fragment || len(name) > len(fragment) && name[len(name)-len(fragment):] == fragment {
			return id
		}
	}
	return ""
}

func graphModulesByName(nodes []CodeGraphNode) map[string]string {
	result := make(map[string]string)
	for _, node := range nodes {
		if node.Kind == "module" && node.Module != nil {
			result[node.Module.Name] = node.ID
		}
	}
	return result
}

func findGraphEdge(edges []CodeGraphEdge, edgeType string) CodeGraphEdge {
	for _, edge := range edges {
		if edge.Type == edgeType {
			return edge
		}
	}
	return CodeGraphEdge{}
}

func writeGraphSource(t *testing.T, root, name, source string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeCodeGraphResolvesRustModulePaths(t *testing.T) {
	root := t.TempDir()
	writeGraphSource(t, root, "src/lib.rs", "mod parser;\npub mod util;\nuse crate::parser::Parser;\nuse std::collections::HashMap;\npub fn run(map: HashMap<i32, Parser>) -> usize { map.len() }\n")
	writeGraphSource(t, root, "src/parser.rs", "use super::run;\npub struct Parser;\n")
	writeGraphSource(t, root, "src/util/mod.rs", "use crate::parser::Parser;\npub fn helper(parser: &Parser) -> bool { true }\n")
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	report, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"src"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	modules := graphModulesByName(report.Nodes)
	if modules["crate"] == "" || modules["crate::parser"] == "" || modules["crate::util"] == "" {
		t.Fatalf("modules = %#v", modules)
	}
	resolutions := make(map[string]string, len(report.References))
	for _, reference := range report.References {
		resolutions[reference.Kind+" "+reference.Specifier] = reference.Resolution + " " + reference.Reason
	}
	want := map[string]string{
		"rust-mod crate::parser":             "resolved ",
		"rust-mod crate::util":               "resolved ",
		"rust-use crate::parser::Parser":     "resolved ",
		"rust-use super::run":                "resolved ",
		"rust-use std::collections::HashMap": "unresolved non-crate-specifier",
	}
	for reference, expected := range want {
		if resolutions[reference] != expected {
			t.Fatalf("reference %q = %q, want %q (all: %#v)", reference, resolutions[reference], expected, resolutions)
		}
	}
}

func TestRustUseDeclarationsExpandToOneReferencePerLeaf(t *testing.T) {
	root := t.TempDir()
	writeGraphSource(t, root, "src/lib.rs", "pub mod a;\npub mod b;\nuse crate::{a::First, b::{Second as Renamed, third::*}};\npub fn run() {}\n")
	writeGraphSource(t, root, "src/a.rs", "pub struct First;\n")
	writeGraphSource(t, root, "src/b/mod.rs", "pub mod third;\npub struct Second;\n")
	writeGraphSource(t, root, "src/b/third.rs", "pub fn value() -> i32 { 1 }\n")
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	report, err := analyzer.AnalyzeCodeGraph(CodeGraphOptions{Root: root, Paths: []string{"src"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	bindings := make(map[string]string)
	for _, reference := range report.References {
		if reference.Kind == "rust-use" || reference.Kind == "rust-use-glob" {
			bindings[reference.Specifier] = reference.Binding
		}
	}
	want := map[string]string{
		"crate::a::First":  "First",
		"crate::b::Second": "Renamed",
		"crate::b::third":  "*",
	}
	if len(bindings) != len(want) {
		t.Fatalf("use references = %#v, want %#v", bindings, want)
	}
	for specifier, binding := range want {
		if bindings[specifier] != binding {
			t.Fatalf("binding for %q = %q, want %q", specifier, bindings[specifier], binding)
		}
	}
}
