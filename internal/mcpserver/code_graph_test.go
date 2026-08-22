package mcpserver

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/rootauth"
)

func TestCodeGraphSnapshotPagesAndNeighborhoodAreImmutable(t *testing.T) {
	root := t.TempDir()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	report := analysis.CodeGraphReport{
		SchemaVersion: "1", ReportType: "code-graph",
		Nodes: []analysis.CodeGraphNode{
			{ID: "file", Kind: "file", Name: "work.go", Path: "work.go"},
			{ID: "type", Kind: "type", Name: "work.Type", Path: "work.go"},
			{ID: "call", Kind: "callable", Name: "work.Type.Run", Path: "work.go"},
		},
		Edges: []analysis.CodeGraphEdge{
			{ID: "edge-1", Type: "contains", From: "file", To: "type"},
			{ID: "edge-2", Type: "declares", From: "type", To: "call"},
		},
		Limitations: []string{}, Diagnostics: []analysis.Diagnostic{},
	}
	store := newSnapshotStore()
	factory := func() (analyzerExecution, error) { return &fakeAnalyzer{calls: calls, graphReport: report}, nil }
	_, first, err := analyzeCodeGraphWith(context.Background(), nil, AnalyzeCodeGraphInput{Root: root, ResultMode: "nodes", Limit: intPointer(1)}, policy, store, factory)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(first.Nodes) != 1 || first.Page.NextCursor == "" || first.ReportID == "" {
		t.Fatalf("first = %#v, calls = %d", first, calls.Load())
	}
	report.Nodes[1].Name = "mutated"
	second, err := getCodeGraph(context.Background(), store, GetCodeGraphInput{Cursor: first.Page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(second.Nodes) != 1 || second.Nodes[0].Name != "work.Type" {
		t.Fatalf("second = %#v, calls = %d", second, calls.Load())
	}
	depth := 2
	neighborhood, err := getCodeGraphNeighborhood(context.Background(), store, GetCodeGraphNeighborhoodInput{
		ReportID: first.ReportID, SeedNodeIDs: []string{"file"}, Direction: "outgoing", Depth: &depth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if neighborhood.ReportID != first.ReportID || len(neighborhood.Neighborhood.Nodes) != 3 || len(neighborhood.Neighborhood.Edges) != 2 {
		t.Fatalf("neighborhood = %#v", neighborhood)
	}
	referenceReport := analysis.CodeGraphReport{SchemaVersion: "1", ReportType: "code-graph", References: []analysis.CodeGraphReference{{ID: "reference", Specifier: "external"}}, Limitations: []string{}, Diagnostics: []analysis.Diagnostic{}}
	referenceItem, err := store.putCodeGraphContext(context.Background(), referenceReport)
	if err != nil {
		t.Fatal(err)
	}
	referencePage, err := getCodeGraph(context.Background(), store, GetCodeGraphInput{ReportID: referenceItem.id, ResultMode: "references"})
	if err != nil {
		t.Fatal(err)
	}
	if len(referencePage.References) != 1 || referencePage.References[0].Specifier != "external" {
		t.Fatalf("reference page = %#v", referencePage)
	}
	if _, err := getAnalysisResults(context.Background(), store, GetResultsInput{ReportID: first.ReportID}); err == nil {
		t.Fatal("code graph snapshot was accepted as analysis")
	}
}

func TestCodeGraphMCPRejectsInvalidQueries(t *testing.T) {
	root := t.TempDir()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	store := newSnapshotStore()
	factory := func() (analyzerExecution, error) { return &fakeAnalyzer{calls: &atomic.Int32{}}, nil }
	if _, _, err := analyzeCodeGraphWith(context.Background(), nil, AnalyzeCodeGraphInput{Root: root, ResultMode: "all"}, policy, store, factory); err == nil {
		t.Fatal("invalid result mode was accepted")
	}
	if _, err := getCodeGraph(context.Background(), store, GetCodeGraphInput{}); err == nil {
		t.Fatal("missing report ID was accepted")
	}
	if _, err := getCodeGraphNeighborhood(context.Background(), store, GetCodeGraphNeighborhoodInput{}); err == nil {
		t.Fatal("missing neighborhood report ID was accepted")
	}
	item, err := store.putContext(context.Background(), analysis.Report{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getCodeGraph(context.Background(), store, GetCodeGraphInput{ReportID: item.id}); err == nil || !strings.Contains(err.Error(), "not a code graph") {
		t.Fatalf("wrong snapshot kind error = %v", err)
	}
}
