package analysis

import (
	"context"
	"testing"
)

func TestBuildCodeGraphNeighborhoodIsCoherentAndDeterministic(t *testing.T) {
	report := CodeGraphReport{
		Nodes: []CodeGraphNode{
			{ID: "call", Kind: "callable"},
			{ID: "file", Kind: "file"},
			{ID: "nested", Kind: "callable"},
			{ID: "type", Kind: "type"},
		},
		Edges: []CodeGraphEdge{
			{ID: "3", Type: "contains", From: "call", To: "nested"},
			{ID: "1", Type: "contains", From: "file", To: "type"},
			{ID: "2", Type: "declares", From: "type", To: "call"},
		},
	}
	options := CodeGraphNeighborhoodOptions{SeedNodeIDs: []string{"file"}, Direction: "outgoing", Depth: 3, MaximumNodes: 3, MaximumEdges: 10}
	first, err := BuildCodeGraphNeighborhood(report, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCodeGraphNeighborhood(report, options)
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary.Nodes != 3 || first.Summary.Edges != 2 || !first.Summary.Truncated || first.Summary.OmittedNodes != 1 || first.Summary.OmittedEdges != 1 {
		t.Fatalf("summary = %#v", first.Summary)
	}
	if len(first.Nodes) != 3 || first.Nodes[0].Node.ID != "file" || first.Nodes[0].Distance != 0 || first.Nodes[2].Node.ID != "call" || first.Nodes[2].Distance != 2 {
		t.Fatalf("nodes = %#v", first.Nodes)
	}
	present := map[string]bool{}
	for _, node := range first.Nodes {
		present[node.Node.ID] = true
	}
	for _, edge := range first.Edges {
		if !present[edge.From] || !present[edge.To] {
			t.Fatalf("dangling edge = %#v", edge)
		}
	}
	if first.Edges[0].ID != second.Edges[0].ID || first.Nodes[2].Node.ID != second.Nodes[2].Node.ID {
		t.Fatalf("results differ: %#v %#v", first, second)
	}
}

func TestBuildCodeGraphNeighborhoodSupportsIncomingAndDepthZero(t *testing.T) {
	report := CodeGraphReport{
		Nodes: []CodeGraphNode{{ID: "file"}, {ID: "type"}, {ID: "call"}},
		Edges: []CodeGraphEdge{
			{ID: "1", Type: "contains", From: "file", To: "type"},
			{ID: "2", Type: "declares", From: "type", To: "call"},
		},
	}
	incoming, err := BuildCodeGraphNeighborhood(report, CodeGraphNeighborhoodOptions{SeedNodeIDs: []string{"call"}, Direction: "incoming", Depth: 2, MaximumNodes: 10, MaximumEdges: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(incoming.Nodes) != 3 || incoming.Nodes[2].Node.ID != "file" || incoming.Nodes[2].Distance != 2 {
		t.Fatalf("incoming = %#v", incoming)
	}
	zero, err := BuildCodeGraphNeighborhood(report, CodeGraphNeighborhoodOptions{SeedNodeIDs: []string{"call"}, Direction: "both", Depth: 0, MaximumNodes: 10, MaximumEdges: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(zero.Nodes) != 1 || len(zero.Edges) != 0 || zero.Summary.Truncated {
		t.Fatalf("depth zero = %#v", zero)
	}
}

func TestBuildCodeGraphNeighborhoodCarriesModuleDependencyReferences(t *testing.T) {
	report := CodeGraphReport{
		Nodes:      []CodeGraphNode{{ID: "left", Kind: "module"}, {ID: "right", Kind: "module"}},
		Edges:      []CodeGraphEdge{{ID: "dependency", Type: "imports", From: "left", To: "right", References: []string{"reference"}}},
		References: []CodeGraphReference{{ID: "reference", Resolution: "resolved", Target: "right"}},
	}
	result, err := BuildCodeGraphNeighborhood(report, CodeGraphNeighborhoodOptions{
		SeedNodeIDs: []string{"left"}, Direction: "outgoing", Depth: 1, EdgeTypes: []string{"imports"}, MaximumNodes: 10, MaximumEdges: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || len(result.Edges) != 1 || len(result.References) != 1 || result.References[0].ID != "reference" || result.Summary.References != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestBuildCodeGraphNeighborhoodEdgeBudgetKeepsNodesConnected(t *testing.T) {
	report := CodeGraphReport{
		Nodes: []CodeGraphNode{{ID: "file"}, {ID: "type"}, {ID: "call"}},
		Edges: []CodeGraphEdge{
			{ID: "1", Type: "contains", From: "file", To: "type"},
			{ID: "2", Type: "declares", From: "type", To: "call"},
		},
	}
	result, err := BuildCodeGraphNeighborhood(report, CodeGraphNeighborhoodOptions{SeedNodeIDs: []string{"file"}, Direction: "outgoing", Depth: 2, MaximumNodes: 10, MaximumEdges: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || len(result.Edges) != 1 || result.Nodes[1].Node.ID != "type" || result.Edges[0].From != "file" || result.Edges[0].To != "type" {
		t.Fatalf("disconnected neighborhood = %#v", result)
	}
	if !result.Summary.Truncated || result.Summary.OmittedNodes != 1 || result.Summary.OmittedEdges != 1 {
		t.Fatalf("summary = %#v", result.Summary)
	}
}

func TestBuildCodeGraphNeighborhoodOptionalEdgesRespectBudgetAndCanonicalOrder(t *testing.T) {
	report := CodeGraphReport{
		Nodes: []CodeGraphNode{{ID: "left"}, {ID: "right"}},
		Edges: []CodeGraphEdge{
			{ID: "z", Type: "contains", From: "left", To: "right"},
			{ID: "a", Type: "declares", From: "left", To: "right"},
		},
	}
	result, err := BuildCodeGraphNeighborhood(report, CodeGraphNeighborhoodOptions{
		SeedNodeIDs: []string{"right", "left"}, Direction: "outgoing", Depth: 0, MaximumNodes: 2, MaximumEdges: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Edges) != 1 || result.Edges[0].ID != "a" {
		t.Fatalf("edges = %#v", result.Edges)
	}
	if !result.Summary.Truncated || result.Summary.OmittedNodes != 0 || result.Summary.OmittedEdges != 1 {
		t.Fatalf("summary = %#v", result.Summary)
	}
}

func TestBuildCodeGraphNeighborhoodRejectsInvalidInputsAndCancellation(t *testing.T) {
	report := CodeGraphReport{Nodes: []CodeGraphNode{{ID: "seed"}}}
	cases := []CodeGraphNeighborhoodOptions{
		{},
		{SeedNodeIDs: []string{"missing"}},
		{SeedNodeIDs: []string{"seed"}, Direction: "sideways"},
		{SeedNodeIDs: []string{"seed"}, Depth: MaximumCodeGraphNeighborhoodDepth + 1},
		{SeedNodeIDs: []string{"seed"}, EdgeTypes: []string{"calls"}},
		{SeedNodeIDs: []string{"seed"}, MaximumNodes: MaximumCodeGraphNeighborhoodNodes + 1},
		{SeedNodeIDs: []string{"seed"}, MaximumEdges: MaximumCodeGraphNeighborhoodEdges + 1},
	}
	for _, options := range cases {
		if _, err := BuildCodeGraphNeighborhood(report, options); err == nil {
			t.Fatalf("accepted %#v", options)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildCodeGraphNeighborhoodContext(ctx, report, CodeGraphNeighborhoodOptions{SeedNodeIDs: []string{"seed"}}); err == nil {
		t.Fatal("canceled neighborhood completed")
	}
}
