package analysis

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
)

const (
	CodeGraphSchemaVersion     = "1"
	MaximumCodeGraphNodes      = 100_000
	MaximumCodeGraphEdges      = 250_000
	MaximumCodeGraphReferences = 250_000
	MaximumCodeGraphDepth      = 64
)

type CodeGraphOptions struct {
	Paths            []string
	CoveragePath     string
	Root             string
	CRAPThreshold    float64
	IncludeTests     bool
	IncludeGenerated bool
	Exclude          []string
	StrictCoverage   bool
	Authorization    *rootauth.Root
}

type CodeGraphReport struct {
	SchemaVersion    string                           `json:"schemaVersion"`
	ReportType       string                           `json:"reportType"`
	Tool             reportcontract.ToolIdentity      `json:"tool"`
	Fingerprints     reportcontract.Fingerprints      `json:"fingerprints"`
	Coordinates      reportcontract.Coordinates       `json:"coordinates"`
	Grammars         []GrammarIdentity                `json:"grammars"`
	Coverage         CoverageMetadata                 `json:"coverage"`
	Discovery        DiscoveryMetadata                `json:"discovery"`
	Threshold        float64                          `json:"threshold"`
	Policy           CodeGraphPolicy                  `json:"policy"`
	Summary          CodeGraphSummary                 `json:"summary"`
	Nodes            []CodeGraphNode                  `json:"nodes"`
	Edges            []CodeGraphEdge                  `json:"edges"`
	References       []CodeGraphReference             `json:"references"`
	ResolutionInputs []reportcontract.FileFingerprint `json:"resolutionInputs"`
	Limitations      []string                         `json:"limitations"`
	Diagnostics      []Diagnostic                     `json:"diagnostics"`
}

type CodeGraphPolicy struct {
	Identity   string `json:"identity"`
	Ownership  string `json:"ownership"`
	Resolution string `json:"resolution"`
}

type CodeGraphSummary struct {
	Files                int `json:"files"`
	Modules              int `json:"modules"`
	Types                int `json:"types"`
	Callables            int `json:"callables"`
	Nodes                int `json:"nodes"`
	ContainsEdges        int `json:"containsEdges"`
	DeclaresEdges        int `json:"declaresEdges"`
	MemberOfEdges        int `json:"memberOfEdges"`
	ImportsEdges         int `json:"importsEdges"`
	Edges                int `json:"edges"`
	References           int `json:"references"`
	ResolvedReferences   int `json:"resolvedReferences"`
	UnresolvedReferences int `json:"unresolvedReferences"`
	AmbiguousReferences  int `json:"ambiguousReferences"`
	AboveThreshold       int `json:"aboveThreshold"`
}

type CodeGraphNode struct {
	ID              string                   `json:"id"`
	Kind            string                   `json:"kind"`
	Language        string                   `json:"language"`
	Name            string                   `json:"name"`
	Path            string                   `json:"path"`
	DeclarationKind string                   `json:"declarationKind,omitempty"`
	Location        *CodeGraphLocation       `json:"location"`
	Metrics         *CodeGraphMetrics        `json:"metrics"`
	Module          *CodeGraphModuleIdentity `json:"module,omitempty"`
	ModuleMetrics   *CodeGraphModuleMetrics  `json:"moduleMetrics,omitempty"`
}

type CodeGraphModuleIdentity struct {
	System  string `json:"system"`
	Name    string `json:"name"`
	Variant string `json:"variant,omitempty"`
}

type CodeGraphModuleMetrics struct {
	Files             int      `json:"files"`
	Types             int      `json:"types"`
	Callables         int      `json:"callables"`
	ComplexityTotal   int      `json:"complexityTotal"`
	ComplexityMaximum int      `json:"complexityMaximum"`
	CoverageKnown     int      `json:"coverageKnown"`
	CoverageUnknown   int      `json:"coverageUnknown"`
	CoverageMean      *float64 `json:"coverageMean"`
	CRAPMaximum       float64  `json:"crapMaximum"`
	AboveThreshold    int      `json:"aboveThreshold"`
}

type CodeGraphLocation struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

type CodeGraphMetrics struct {
	Complexity      int      `json:"complexity"`
	CoveragePercent *float64 `json:"coveragePercent"`
	CRAP            float64  `json:"crap"`
	AboveThreshold  bool     `json:"aboveThreshold"`
}

type CodeGraphEdge struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	Resolution  string   `json:"resolution"`
	Evidence    string   `json:"evidence"`
	Occurrences int      `json:"occurrences"`
	References  []string `json:"references"`
}

type CodeGraphReference struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Language     string            `json:"language"`
	SourceFile   string            `json:"sourceFile"`
	SourceModule string            `json:"sourceModule"`
	Specifier    string            `json:"specifier"`
	Scope        string            `json:"scope,omitempty"`
	Binding      string            `json:"binding,omitempty"`
	Location     CodeGraphLocation `json:"location"`
	Resolution   string            `json:"resolution"`
	Target       string            `json:"target,omitempty"`
	Candidates   []string          `json:"candidates"`
	Reason       string            `json:"reason,omitempty"`
}

func (analyzer *Analyzer) AnalyzeCodeGraph(options CodeGraphOptions) (CodeGraphReport, error) {
	return analyzer.AnalyzeCodeGraphContext(context.Background(), options)
}

func (analyzer *Analyzer) AnalyzeCodeGraphContext(ctx context.Context, options CodeGraphOptions) (CodeGraphReport, error) {
	report, inputs, err := analyzer.analyzeContext(ctx, Options{
		Paths: options.Paths, CoveragePath: options.CoveragePath, Root: options.Root, CRAPThreshold: options.CRAPThreshold,
		IncludeTests: options.IncludeTests, IncludeGenerated: options.IncludeGenerated, Exclude: options.Exclude,
		StrictCoverage: options.StrictCoverage, Authorization: options.Authorization,
	})
	if err != nil {
		return CodeGraphReport{}, err
	}
	graph := newCodeGraphReport(report)
	if err := buildCodeGraph(ctx, &graph, inputs, report.Methods); err != nil {
		return CodeGraphReport{}, err
	}
	return graph, nil
}

func newCodeGraphReport(report Report) CodeGraphReport {
	fingerprints := report.Fingerprints
	fingerprints.ConfigSHA256 = reportcontract.JSONFingerprint(struct {
		Contract          string `json:"contract"`
		AnalysisConfig    string `json:"analysisConfig"`
		MaximumNodes      int    `json:"maximumNodes"`
		MaximumEdges      int    `json:"maximumEdges"`
		MaximumReferences int    `json:"maximumReferences"`
		MaximumDepth      int    `json:"maximumDepth"`
	}{CodeGraphSchemaVersion, report.Fingerprints.ConfigSHA256, MaximumCodeGraphNodes, MaximumCodeGraphEdges, MaximumCodeGraphReferences, MaximumCodeGraphDepth})
	return CodeGraphReport{
		SchemaVersion: CodeGraphSchemaVersion, ReportType: "code-graph", Tool: report.Tool,
		Fingerprints: fingerprints, Coordinates: report.Coordinates, Grammars: append([]GrammarIdentity(nil), report.Grammars...),
		Coverage: report.Coverage, Discovery: report.Discovery, Threshold: report.Threshold,
		Policy: CodeGraphPolicy{Identity: "declaration-occurrence-and-logical-module-v1", Ownership: "nearest-lexical-parent", Resolution: "bounded-selected-source-v1"},
		Nodes:  make([]CodeGraphNode, 0), Edges: make([]CodeGraphEdge, 0), References: make([]CodeGraphReference, 0), ResolutionInputs: make([]reportcontract.FileFingerprint, 0),
		Limitations: []string{
			"type nodes represent source declaration occurrences, not compiler-resolved semantic types",
			"relationships are limited to exact lexical containment and declaration ownership",
			"Go receiver-to-type associations and cross-file declaration merging are not modeled",
			"static imports and using directives resolve only against selected repository sources",
			"Go workspaces, replacements, build tags, vendor rules, TypeScript configuration, and C# assembly binding are not modeled",
			"calls, runtime dispatch, and behavioral impact are not modeled",
		},
		Diagnostics: append(make([]Diagnostic, 0, len(report.Diagnostics)), report.Diagnostics...),
	}
}

func buildCodeGraph(ctx context.Context, graph *CodeGraphReport, inputs analysisInputs, methods []MethodResult) error {
	methodsByFile := make(map[string][]MethodResult)
	for _, method := range methods {
		methodsByFile[method.File] = append(methodsByFile[method.File], method)
	}
	for _, source := range graph.Fingerprints.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		structure, ok := inputs.structures[source.Path]
		if !ok {
			return fmt.Errorf("code graph structure missing for %s", source.Path)
		}
		if err := appendFileGraph(graph, source.Path, structure, methodsByFile[source.Path]); err != nil {
			return err
		}
	}
	if err := buildModuleGraph(ctx, graph, inputs); err != nil {
		return err
	}
	if len(graph.Nodes) > MaximumCodeGraphNodes {
		return fmt.Errorf("code graph has %d nodes; maximum %d", len(graph.Nodes), MaximumCodeGraphNodes)
	}
	if len(graph.Edges) > MaximumCodeGraphEdges {
		return fmt.Errorf("code graph has %d edges; maximum %d", len(graph.Edges), MaximumCodeGraphEdges)
	}
	if len(graph.References) > MaximumCodeGraphReferences {
		return fmt.Errorf("code graph has %d references; maximum %d", len(graph.References), MaximumCodeGraphReferences)
	}
	sort.Slice(graph.Nodes, func(i, j int) bool { return graph.Nodes[i].ID < graph.Nodes[j].ID })
	sort.Slice(graph.Edges, func(i, j int) bool { return graph.Edges[i].ID < graph.Edges[j].ID })
	sort.Slice(graph.References, func(i, j int) bool { return graph.References[i].ID < graph.References[j].ID })
	return validateAndSummarizeCodeGraph(ctx, graph)
}

func appendFileGraph(graph *CodeGraphReport, path string, structure fileStructure, methods []MethodResult) error {
	fileID := reportcontract.Fingerprint("code-graph-file-v1", path)
	graph.Nodes = append(graph.Nodes, CodeGraphNode{ID: fileID, Kind: "file", Language: structure.language, Name: filepath.Base(path), Path: path})
	typeIDs := make([]string, len(structure.types))
	for index, declaration := range structure.types {
		typeIDs[index] = declaration.ID
		graph.Nodes = append(graph.Nodes, CodeGraphNode{
			ID: declaration.ID, Kind: "type", Language: structure.language, Name: declaration.Name, Path: path, DeclarationKind: declaration.Kind,
			Location: &CodeGraphLocation{StartLine: declaration.StartLine, StartColumn: declaration.StartColumn, EndLine: declaration.EndLine, EndColumn: declaration.EndColumn},
		})
	}
	methodByID := make(map[string]MethodResult, len(methods))
	for _, method := range methods {
		methodByID[method.ID] = method
		graph.Nodes = append(graph.Nodes, CodeGraphNode{
			ID: method.ID, Kind: "callable", Language: method.Language, Name: method.Name, Path: method.File, DeclarationKind: method.Kind,
			Location: &CodeGraphLocation{StartLine: method.StartLine, StartColumn: method.StartColumn, EndLine: method.EndLine, EndColumn: method.EndColumn},
			Metrics:  &CodeGraphMetrics{Complexity: method.Complexity, CoveragePercent: method.CoveragePercent, CRAP: method.CRAP, AboveThreshold: method.AboveThreshold},
		})
	}
	callableIDs := make([]string, len(structure.callables))
	for index, callable := range structure.callables {
		if _, ok := methodByID[callable.ID]; !ok {
			return fmt.Errorf("code graph callable %s missing analysis result", callable.ID)
		}
		callableIDs[index] = callable.ID
	}
	for index, declaration := range structure.types {
		parent := fileID
		if declaration.ParentKind == "type" {
			parent = typeIDs[declaration.ParentIndex]
		} else if declaration.ParentKind == "callable" {
			parent = callableIDs[declaration.ParentIndex]
		}
		graph.Edges = append(graph.Edges, newCodeGraphEdge("contains", parent, typeIDs[index]))
	}
	for index, callable := range structure.callables {
		parent, edgeType := fileID, "contains"
		if callable.ParentKind == "callable" {
			parent = callableIDs[callable.ParentIndex]
		} else if callable.ParentKind == "type" {
			parent, edgeType = typeIDs[callable.ParentIndex], "declares"
		}
		graph.Edges = append(graph.Edges, newCodeGraphEdge(edgeType, parent, callableIDs[index]))
	}
	return nil
}

func newCodeGraphEdge(edgeType, from, to string) CodeGraphEdge {
	return CodeGraphEdge{
		ID: reportcontract.Fingerprint("code-graph-edge-v1", edgeType, from, to), Type: edgeType,
		From: from, To: to, Resolution: "exact", Evidence: "lexical-ast", Occurrences: 1, References: make([]string, 0),
	}
}

func validateAndSummarizeCodeGraph(ctx context.Context, graph *CodeGraphReport) error {
	nodes, err := indexAndSummarizeCodeGraphNodes(ctx, graph)
	if err != nil {
		return err
	}
	lexicalEdges, err := indexAndSummarizeCodeGraphEdges(nodes, graph)
	if err != nil {
		return err
	}
	if err := validateCodeGraphForest(ctx, nodes, lexicalEdges); err != nil {
		return err
	}
	if err := validateCodeGraphReferences(ctx, graph, nodes, graph.Edges, graph.References); err != nil {
		return err
	}
	graph.Summary.Nodes, graph.Summary.Edges = len(graph.Nodes), len(graph.Edges)
	graph.Summary.References = len(graph.References)
	return ctx.Err()
}

func indexAndSummarizeCodeGraphNodes(ctx context.Context, graph *CodeGraphReport) (map[string]CodeGraphNode, error) {
	nodes := make(map[string]CodeGraphNode, len(graph.Nodes))
	for _, node := range graph.Nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, duplicate := nodes[node.ID]; duplicate {
			return nil, fmt.Errorf("duplicate code graph node %s", node.ID)
		}
		nodes[node.ID] = node
		if err := summarizeCodeGraphNode(&graph.Summary, node); err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func summarizeCodeGraphNode(summary *CodeGraphSummary, node CodeGraphNode) error {
	switch node.Kind {
	case "file":
		summary.Files++
	case "module":
		if node.Module == nil || node.ModuleMetrics == nil {
			return fmt.Errorf("code graph module node %s lacks module metadata", node.ID)
		}
		summary.Modules++
	case "type":
		summary.Types++
	case "callable":
		summary.Callables++
		if node.Metrics != nil && node.Metrics.AboveThreshold {
			summary.AboveThreshold++
		}
	default:
		return fmt.Errorf("unsupported code graph node kind %q", node.Kind)
	}
	return nil
}

func indexAndSummarizeCodeGraphEdges(nodes map[string]CodeGraphNode, graph *CodeGraphReport) ([]CodeGraphEdge, error) {
	edges := make(map[string]bool, len(graph.Edges))
	lexicalEdges := make([]CodeGraphEdge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		if nodes[edge.From].ID == "" || nodes[edge.To].ID == "" {
			return nil, fmt.Errorf("code graph edge %s has a missing endpoint", edge.ID)
		}
		if edges[edge.ID] {
			return nil, fmt.Errorf("duplicate code graph edge %s", edge.ID)
		}
		edges[edge.ID] = true
		lexical, err := summarizeCodeGraphEdge(&graph.Summary, nodes, edge)
		if err != nil {
			return nil, err
		}
		if lexical {
			lexicalEdges = append(lexicalEdges, edge)
		}
	}
	return lexicalEdges, nil
}

func summarizeCodeGraphEdge(summary *CodeGraphSummary, nodes map[string]CodeGraphNode, edge CodeGraphEdge) (bool, error) {
	switch edge.Type {
	case "contains":
		summary.ContainsEdges++
		return true, nil
	case "declares":
		summary.DeclaresEdges++
		return true, nil
	case "member-of":
		if nodes[edge.From].Kind != "file" || nodes[edge.To].Kind != "module" {
			return false, fmt.Errorf("code graph edge %s has invalid module membership", edge.ID)
		}
		summary.MemberOfEdges++
	case "imports":
		if nodes[edge.From].Kind != "module" || nodes[edge.To].Kind != "module" {
			return false, fmt.Errorf("code graph edge %s has invalid module dependency", edge.ID)
		}
		summary.ImportsEdges++
	default:
		return false, fmt.Errorf("unsupported code graph edge type %q", edge.Type)
	}
	return false, nil
}

type codeGraphDepthEntry struct {
	id    string
	depth int
}

func validateCodeGraphForest(ctx context.Context, nodes map[string]CodeGraphNode, edges []CodeGraphEdge) error {
	parents, children, err := indexCodeGraphForestEdges(nodes, edges)
	if err != nil {
		return err
	}
	roots, lexicalNodes, err := codeGraphForestRoots(nodes, parents)
	if err != nil {
		return err
	}
	return validateCodeGraphForestDepth(ctx, roots, children, lexicalNodes)
}

func indexCodeGraphForestEdges(nodes map[string]CodeGraphNode, edges []CodeGraphEdge) (map[string]int, map[string][]string, error) {
	parents := make(map[string]int, len(nodes))
	children := make(map[string][]string)
	for _, edge := range edges {
		parent, child := nodes[edge.From], nodes[edge.To]
		if edge.From == edge.To {
			return nil, nil, fmt.Errorf("code graph edge %s is a self-edge", edge.ID)
		}
		if edge.Type == "declares" && (parent.Kind != "type" || child.Kind != "callable") {
			return nil, nil, fmt.Errorf("code graph edge %s has invalid declaration ownership", edge.ID)
		}
		if edge.Type == "contains" && !validCodeGraphContainment(parent.Kind, child.Kind) {
			return nil, nil, fmt.Errorf("code graph edge %s has invalid containment", edge.ID)
		}
		parents[edge.To]++
		if parents[edge.To] > 1 {
			return nil, nil, fmt.Errorf("code graph node %s has multiple parents", edge.To)
		}
		children[edge.From] = append(children[edge.From], edge.To)
	}
	return parents, children, nil
}

func codeGraphForestRoots(nodes map[string]CodeGraphNode, parents map[string]int) ([]string, int, error) {
	roots := make([]string, 0)
	nodeIDs := make([]string, 0, len(nodes))
	lexicalNodes := 0
	for id := range nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	for _, id := range nodeIDs {
		node := nodes[id]
		if node.Kind == "module" {
			continue
		}
		lexicalNodes++
		if node.Kind == "file" {
			if parents[node.ID] != 0 {
				return nil, 0, fmt.Errorf("code graph file node %s has a parent", node.ID)
			}
			roots = append(roots, node.ID)
		} else if parents[node.ID] != 1 {
			return nil, 0, fmt.Errorf("code graph node %s has no parent", node.ID)
		}
	}
	sort.Strings(roots)
	return roots, lexicalNodes, nil
}

func validateCodeGraphForestDepth(ctx context.Context, roots []string, children map[string][]string, lexicalNodes int) error {
	seen := make(map[string]bool, lexicalNodes)
	stack := make([]codeGraphDepthEntry, 0, len(roots))
	for index := len(roots) - 1; index >= 0; index-- {
		stack = append(stack, codeGraphDepthEntry{id: roots[index], depth: 1})
	}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if entry.depth > MaximumCodeGraphDepth {
			return fmt.Errorf("code graph containment depth exceeds %d", MaximumCodeGraphDepth)
		}
		if seen[entry.id] {
			return fmt.Errorf("code graph contains a containment cycle")
		}
		seen[entry.id] = true
		nested := children[entry.id]
		sort.Strings(nested)
		for index := len(nested) - 1; index >= 0; index-- {
			stack = append(stack, codeGraphDepthEntry{id: nested[index], depth: entry.depth + 1})
		}
	}
	if len(seen) != lexicalNodes {
		return fmt.Errorf("code graph contains a containment cycle")
	}
	return nil
}

func validateCodeGraphReferences(ctx context.Context, graph *CodeGraphReport, nodes map[string]CodeGraphNode, edges []CodeGraphEdge, references []CodeGraphReference) error {
	referenceByID, err := indexAndSummarizeCodeGraphReferences(ctx, graph, nodes, references)
	if err != nil {
		return err
	}
	resolvedEdges, err := validateCodeGraphDependencyEvidence(edges, referenceByID)
	if err != nil {
		return err
	}
	return validateResolvedCodeGraphReferences(references, resolvedEdges)
}

func indexAndSummarizeCodeGraphReferences(ctx context.Context, graph *CodeGraphReport, nodes map[string]CodeGraphNode, references []CodeGraphReference) (map[string]CodeGraphReference, error) {
	referenceByID := make(map[string]CodeGraphReference, len(references))
	for _, reference := range references {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, duplicate := referenceByID[reference.ID]; duplicate {
			return nil, fmt.Errorf("duplicate code graph reference %s", reference.ID)
		}
		if err := validateAndSummarizeCodeGraphReference(graph, nodes, reference); err != nil {
			return nil, err
		}
		referenceByID[reference.ID] = reference
	}
	return referenceByID, nil
}

func validateAndSummarizeCodeGraphReference(graph *CodeGraphReport, nodes map[string]CodeGraphNode, reference CodeGraphReference) error {
	if nodes[reference.SourceFile].Kind != "file" || nodes[reference.SourceModule].Kind != "module" {
		return fmt.Errorf("code graph reference %s has an invalid source", reference.ID)
	}
	if err := validateCodeGraphReferenceCandidates(nodes, reference); err != nil {
		return err
	}
	return summarizeCodeGraphReferenceResolution(&graph.Summary, nodes, reference)
}

func validateCodeGraphReferenceCandidates(nodes map[string]CodeGraphNode, reference CodeGraphReference) error {
	for _, candidate := range reference.Candidates {
		if nodes[candidate].Kind != "module" {
			return fmt.Errorf("code graph reference %s has an invalid candidate", reference.ID)
		}
	}
	return nil
}

func summarizeCodeGraphReferenceResolution(summary *CodeGraphSummary, nodes map[string]CodeGraphNode, reference CodeGraphReference) error {
	switch reference.Resolution {
	case "resolved":
		if nodes[reference.Target].Kind != "module" || reference.Reason != "" {
			return fmt.Errorf("resolved code graph reference %s lacks one target", reference.ID)
		}
		summary.ResolvedReferences++
	case "unresolved":
		if reference.Target != "" || reference.Reason == "" {
			return fmt.Errorf("unresolved code graph reference %s has invalid evidence", reference.ID)
		}
		summary.UnresolvedReferences++
	case "ambiguous":
		if reference.Target != "" || len(reference.Candidates) < 2 || reference.Reason != "multiple-candidates" {
			return fmt.Errorf("ambiguous code graph reference %s has invalid candidates", reference.ID)
		}
		summary.AmbiguousReferences++
	default:
		return fmt.Errorf("unsupported code graph reference resolution %q", reference.Resolution)
	}
	return nil
}

func validateCodeGraphDependencyEvidence(edges []CodeGraphEdge, referenceByID map[string]CodeGraphReference) (map[string]int, error) {
	resolvedEdges := make(map[string]int)
	for _, edge := range edges {
		if edge.Type != "imports" {
			continue
		}
		for _, referenceID := range edge.References {
			reference, ok := referenceByID[referenceID]
			if !ok || reference.Resolution != "resolved" || reference.SourceModule != edge.From || reference.Target != edge.To {
				return nil, fmt.Errorf("code graph edge %s has invalid reference evidence", edge.ID)
			}
			resolvedEdges[referenceID]++
		}
	}
	return resolvedEdges, nil
}

func validateResolvedCodeGraphReferences(references []CodeGraphReference, resolvedEdges map[string]int) error {
	for _, reference := range references {
		if reference.Resolution == "resolved" && resolvedEdges[reference.ID] != 1 {
			return fmt.Errorf("resolved code graph reference %s does not have one dependency edge", reference.ID)
		}
	}
	return nil
}

func validCodeGraphContainment(parentKind, childKind string) bool {
	if childKind == "file" {
		return false
	}
	switch parentKind {
	case "file", "callable":
		return childKind == "type" || childKind == "callable"
	case "type":
		return childKind == "type"
	default:
		return false
	}
}
