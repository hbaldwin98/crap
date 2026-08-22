package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func graphModuleNode(id, system, name string) CodeGraphNode {
	return CodeGraphNode{ID: id, Kind: "module", Name: name, Module: &CodeGraphModuleIdentity{System: system, Name: name}}
}

func graphModuleEdge(id, from, to string, refs ...string) CodeGraphEdge {
	edge := CodeGraphEdge{ID: id, Type: "imports", From: from, To: to, Evidence: "static-import"}
	edge.References = append(edge.References, refs...)
	return edge
}

func TestAnalyzeArchitectureReportsAcyclicGraph(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		graphModuleNode("a", "go-package", "example.com/app/a"),
		graphModuleNode("b", "go-package", "example.com/app/b"),
		graphModuleNode("c", "go-package", "example.com/app/c"),
	}, Edges: []CodeGraphEdge{
		graphModuleEdge("e1", "a", "b"),
		graphModuleEdge("e2", "b", "c"),
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Cycles != 0 || report.Summary.Violations != 0 || !report.Summary.Complete {
		t.Fatalf("expected no violations, got %#v", report.Summary)
	}
}

func TestAnalyzeArchitectureDetectsCycle(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		graphModuleNode("a", "go-package", "example.com/app/a"),
		graphModuleNode("b", "go-package", "example.com/app/b"),
		graphModuleNode("c", "go-package", "example.com/app/c"),
	}, Edges: []CodeGraphEdge{
		graphModuleEdge("e1", "a", "b", "ref-ab"),
		graphModuleEdge("e2", "b", "c", "ref-bc"),
		graphModuleEdge("e3", "c", "a", "ref-ca"),
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Cycles != 1 || report.Summary.Violations != 1 || report.Summary.Complete {
		t.Fatalf("expected 1 cycle + 1 violation, got %#v", report.Summary)
	}
	if report.Cycles[0].Edges[0].References[0] != "ref-ab" {
		t.Fatalf("expected reference evidence, got %#v", report.Cycles[0])
	}
}

func TestAnalyzeArchitectureDetectsMutualCycle(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		graphModuleNode("a", "go-package", "example.com/app/a"),
		graphModuleNode("b", "go-package", "example.com/app/b"),
		graphModuleNode("c", "go-package", "example.com/app/c"),
	}, Edges: []CodeGraphEdge{
		graphModuleEdge("e1", "a", "b"),
		graphModuleEdge("e2", "b", "a"),
		graphModuleEdge("e3", "b", "c"),
		graphModuleEdge("e4", "c", "b"),
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Cycles != 1 {
		t.Fatalf("expected 1 cycle, got %#v", report.Summary)
	}
}

func TestAnalyzeArchitectureSelfLoopIsCycle(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		graphModuleNode("a", "go-package", "example.com/app/a"),
	}, Edges: []CodeGraphEdge{
		graphModuleEdge("e1", "a", "a"),
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Cycles != 1 {
		t.Fatalf("expected 1 self-loop cycle, got %#v", report.Summary)
	}
}

func TestAnalyzeArchitectureForbidRules(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		graphModuleNode("a", "go-package", "example.com/app/api"),
		graphModuleNode("b", "go-package", "example.com/app/db"),
	}, Edges: []CodeGraphEdge{
		graphModuleEdge("e1", "a", "b"),
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{
		Forbid: []ArchitectureForbid{
			{From: "**/api", To: "**/db", Reason: "api must not depend on db"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Violations != 1 || report.Violations[0].Kind != "forbid" {
		t.Fatalf("expected 1 forbid violation, got %#v", report.Summary)
	}
	if report.Violations[0].Reason != "api must not depend on db" {
		t.Fatalf("reason not propagated: %#v", report.Violations[0])
	}
}

func TestAnalyzeArchitectureForbidRuleAllowsNonMatching(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		graphModuleNode("a", "go-package", "example.com/app/a"),
		graphModuleNode("b", "go-package", "example.com/app/b"),
	}, Edges: []CodeGraphEdge{
		graphModuleEdge("e1", "a", "b"),
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{
		Forbid: []ArchitectureForbid{
			{From: "**/web", To: "**/core"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Violations != 0 {
		t.Fatalf("expected 0 violations, got %#v", report.Summary)
	}
}

func TestAnalyzeArchitectureSkipsNonModuleAndNonImportEdges(t *testing.T) {
	graph := CodeGraphReport{Nodes: []CodeGraphNode{
		{ID: "file", Kind: "file"},
		graphModuleNode("a", "go-package", "example.com/app/a"),
	}, Edges: []CodeGraphEdge{
		{ID: "e1", Type: "contains", From: "file", To: "a"},
		{ID: "e2", Type: "declares", From: "a", To: "file"},
		{ID: "e3", Type: "member-of", From: "file", To: "a"},
	}}
	report, err := AnalyzeArchitecture(context.Background(), graph, ArchitectureRules{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Modules != 1 || report.Summary.Edges != 0 {
		t.Fatalf("expected only module/imports counted, got %#v", report.Summary)
	}
}

func TestAnalyzeArchitectureCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AnalyzeArchitecture(ctx, CodeGraphReport{}, ArchitectureRules{})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestAnalyzeArchitectureInvalidSchema(t *testing.T) {
	_, err := AnalyzeArchitecture(context.Background(), CodeGraphReport{}, ArchitectureRules{SchemaVersion: "999"})
	if err == nil {
		t.Fatal("expected schema error")
	}
}

func TestAnalyzeArchitectureIsDeterministic(t *testing.T) {
	marshal := func(nodes []CodeGraphNode, edges []CodeGraphEdge) string {
		report, err := AnalyzeArchitecture(context.Background(), CodeGraphReport{Nodes: nodes, Edges: edges}, ArchitectureRules{
			Forbid: []ArchitectureForbid{{From: "**/a", To: "**/b"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(report)
		return string(data)
	}
	nodes := []CodeGraphNode{
		graphModuleNode("c", "go-package", "example.com/app/c"),
		graphModuleNode("a", "go-package", "example.com/app/a"),
		graphModuleNode("b", "go-package", "example.com/app/b"),
	}
	graph1 := CodeGraphReport{Nodes: append([]CodeGraphNode(nil), nodes...), Edges: []CodeGraphEdge{graphModuleEdge("e1", "a", "b"), graphModuleEdge("e2", "b", "c"), graphModuleEdge("e3", "c", "a")}}
	// Shuffled deterministically different order.
	graph2 := CodeGraphReport{Nodes: []CodeGraphNode{nodes[1], nodes[2], nodes[0]}, Edges: []CodeGraphEdge{graphModuleEdge("e3", "c", "a"), graphModuleEdge("e1", "a", "b"), graphModuleEdge("e2", "b", "c")}}
	first := marshal(graph1.Nodes, graph1.Edges)
	second := marshal(graph2.Nodes, graph2.Edges)
	if first != second {
		t.Fatal("architecture report is not deterministic across component orders")
	}
}

func TestArchitectureGlob(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/internal", "example.com/app/internal", true},
		{"**/internal", "example.com/app/internal/other", false},
		{"**/api", "example.com/app/api", true},
		{"*/a", "b/a", true},
		{"*/a", "b/c/a", false},
		{"**/db", "example.com/db", true},
		{"**/db", "example.com/db2", false},
		{"", "anything", true},
	}
	for _, test := range cases {
		got := architectureModuleMatches(test.pattern, test.name)
		if got != test.want {
			t.Errorf("architectureModuleMatches(%q, %q) = %v, want %v", test.pattern, test.name, got, test.want)
		}
	}
}

func TestAnalyzeArchitectureRejectsInvalidSchema(t *testing.T) {
	ctx := context.Background()
	_, err1 := AnalyzeArchitecture(ctx, CodeGraphReport{}, ArchitectureRules{SchemaVersion: "2"})
	if err1 == nil || !strings.Contains(err1.Error(), "unsupported") {
		t.Fatalf("expected unsupported schema error, got %v", err1)
	}
}
