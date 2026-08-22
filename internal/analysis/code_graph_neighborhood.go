package analysis

import (
	"context"
	"fmt"
	"sort"
)

const (
	MaximumCodeGraphNeighborhoodSeeds = 20
	MaximumCodeGraphNeighborhoodDepth = 5
	MaximumCodeGraphNeighborhoodNodes = 1_000
	MaximumCodeGraphNeighborhoodEdges = 2_000
)

type CodeGraphNeighborhoodOptions struct {
	SeedNodeIDs  []string
	Direction    string
	Depth        int
	EdgeTypes    []string
	MaximumNodes int
	MaximumEdges int
}

type CodeGraphNeighborhood struct {
	SchemaVersion string                       `json:"schemaVersion"`
	ReportType    string                       `json:"reportType"`
	SeedNodeIDs   []string                     `json:"seedNodeIds"`
	Direction     string                       `json:"direction"`
	Depth         int                          `json:"depth"`
	EdgeTypes     []string                     `json:"edgeTypes"`
	Summary       CodeGraphNeighborhoodSummary `json:"summary"`
	Nodes         []CodeGraphNeighborhoodNode  `json:"nodes"`
	Edges         []CodeGraphEdge              `json:"edges"`
	References    []CodeGraphReference         `json:"references"`
}

type CodeGraphNeighborhoodSummary struct {
	SeedNodes    int  `json:"seedNodes"`
	Nodes        int  `json:"nodes"`
	Edges        int  `json:"edges"`
	References   int  `json:"references"`
	Truncated    bool `json:"truncated"`
	OmittedNodes int  `json:"omittedNodes"`
	OmittedEdges int  `json:"omittedEdges"`
}

type CodeGraphNeighborhoodNode struct {
	Node     CodeGraphNode `json:"node"`
	Distance int           `json:"distance"`
}

type codeGraphAdjacent struct {
	edge     CodeGraphEdge
	neighbor string
}

func BuildCodeGraphNeighborhood(report CodeGraphReport, options CodeGraphNeighborhoodOptions) (CodeGraphNeighborhood, error) {
	return BuildCodeGraphNeighborhoodContext(context.Background(), report, options)
}

func BuildCodeGraphNeighborhoodContext(ctx context.Context, report CodeGraphReport, options CodeGraphNeighborhoodOptions) (CodeGraphNeighborhood, error) {
	resolved, err := normalizeCodeGraphNeighborhoodOptions(options)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	nodes := make(map[string]CodeGraphNode, len(report.Nodes))
	for _, node := range report.Nodes {
		if err := ctx.Err(); err != nil {
			return CodeGraphNeighborhood{}, err
		}
		nodes[node.ID] = node
	}
	for _, seed := range resolved.SeedNodeIDs {
		if _, ok := nodes[seed]; !ok {
			return CodeGraphNeighborhood{}, fmt.Errorf("code graph seed node %s was not found", seed)
		}
	}
	edgeTypes := make(map[string]bool, len(resolved.EdgeTypes))
	for _, edgeType := range resolved.EdgeTypes {
		edgeTypes[edgeType] = true
	}
	adjacency, err := codeGraphAdjacency(ctx, report.Edges, edgeTypes, resolved.Direction)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	distances, predecessors, err := codeGraphDistances(ctx, resolved.SeedNodeIDs, adjacency, resolved.Depth)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	orderedNodeIDs := make([]string, 0, len(distances))
	for id := range distances {
		if err := ctx.Err(); err != nil {
			return CodeGraphNeighborhood{}, err
		}
		orderedNodeIDs = append(orderedNodeIDs, id)
	}
	if err := ctx.Err(); err != nil {
		return CodeGraphNeighborhood{}, err
	}
	sort.Slice(orderedNodeIDs, func(i, j int) bool {
		left, right := distances[orderedNodeIDs[i]], distances[orderedNodeIDs[j]]
		if left != right {
			return left < right
		}
		return orderedNodeIDs[i] < orderedNodeIDs[j]
	})
	if err := ctx.Err(); err != nil {
		return CodeGraphNeighborhood{}, err
	}
	selected, requiredEdges, resultNodes, err := selectCodeGraphNeighborhoodNodes(ctx, nodes, orderedNodeIDs, distances, predecessors, resolved.MaximumNodes, resolved.MaximumEdges)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	reachableEdges := make([]CodeGraphEdge, 0)
	optionalEdges := make([]CodeGraphEdge, 0)
	for _, edge := range report.Edges {
		if err := ctx.Err(); err != nil {
			return CodeGraphNeighborhood{}, err
		}
		if !edgeTypes[edge.Type] || !codeGraphEdgeWithinDirection(edge, distances, resolved.Direction) {
			continue
		}
		reachableEdges = append(reachableEdges, edge)
		if selected[edge.From] && selected[edge.To] && requiredEdges[edge.ID].ID == "" {
			optionalEdges = append(optionalEdges, edge)
		}
	}
	sort.Slice(optionalEdges, func(i, j int) bool { return optionalEdges[i].ID < optionalEdges[j].ID })
	if err := ctx.Err(); err != nil {
		return CodeGraphNeighborhood{}, err
	}
	selectedEdges := make([]CodeGraphEdge, 0, resolved.MaximumEdges)
	for _, edge := range requiredEdges {
		selectedEdges = append(selectedEdges, edge)
	}
	remaining := resolved.MaximumEdges - len(selectedEdges)
	if len(optionalEdges) > remaining {
		optionalEdges = optionalEdges[:remaining]
	}
	selectedEdges = append(selectedEdges, optionalEdges...)
	sort.Slice(selectedEdges, func(i, j int) bool { return selectedEdges[i].ID < selectedEdges[j].ID })
	omittedNodes := len(orderedNodeIDs) - len(resultNodes)
	omittedEdges := len(reachableEdges) - len(selectedEdges)
	selectedReferences := codeGraphNeighborhoodReferences(report.References, selectedEdges)
	return CodeGraphNeighborhood{
		SchemaVersion: "1", ReportType: "code-graph-neighborhood", SeedNodeIDs: resolved.SeedNodeIDs,
		Direction: resolved.Direction, Depth: resolved.Depth, EdgeTypes: resolved.EdgeTypes,
		Summary: CodeGraphNeighborhoodSummary{
			SeedNodes: len(resolved.SeedNodeIDs), Nodes: len(resultNodes), Edges: len(selectedEdges), References: len(selectedReferences),
			Truncated: omittedNodes > 0 || omittedEdges > 0, OmittedNodes: omittedNodes, OmittedEdges: omittedEdges,
		},
		Nodes: resultNodes, Edges: selectedEdges, References: selectedReferences,
	}, nil
}

func selectCodeGraphNeighborhoodNodes(ctx context.Context, nodes map[string]CodeGraphNode, orderedNodeIDs []string, distances map[string]int, predecessors map[string]codeGraphAdjacent, maximumNodes, maximumEdges int) (map[string]bool, map[string]CodeGraphEdge, []CodeGraphNeighborhoodNode, error) {
	selected := make(map[string]bool, min(len(orderedNodeIDs), maximumNodes))
	requiredEdges := make(map[string]CodeGraphEdge)
	resultNodes := make([]CodeGraphNeighborhoodNode, 0, min(len(orderedNodeIDs), maximumNodes))
	for _, id := range orderedNodeIDs {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if len(resultNodes) >= maximumNodes {
			break
		}
		if distances[id] > 0 {
			predecessor := predecessors[id]
			if !selected[predecessor.neighbor] || len(requiredEdges) >= maximumEdges {
				continue
			}
			requiredEdges[predecessor.edge.ID] = predecessor.edge
		}
		selected[id] = true
		resultNodes = append(resultNodes, CodeGraphNeighborhoodNode{Node: nodes[id], Distance: distances[id]})
	}
	return selected, requiredEdges, resultNodes, nil
}

func normalizeCodeGraphNeighborhoodOptions(options CodeGraphNeighborhoodOptions) (CodeGraphNeighborhoodOptions, error) {
	options.SeedNodeIDs = sortedUniqueStrings(options.SeedNodeIDs)
	if len(options.SeedNodeIDs) == 0 || len(options.SeedNodeIDs) > MaximumCodeGraphNeighborhoodSeeds {
		return options, fmt.Errorf("seedNodeIds must contain 1 through %d unique IDs", MaximumCodeGraphNeighborhoodSeeds)
	}
	if options.Direction == "" {
		options.Direction = "both"
	}
	if options.Direction != "incoming" && options.Direction != "outgoing" && options.Direction != "both" {
		return options, fmt.Errorf("direction must be incoming, outgoing, or both")
	}
	if options.Depth < 0 || options.Depth > MaximumCodeGraphNeighborhoodDepth {
		return options, fmt.Errorf("depth must be between 0 and %d", MaximumCodeGraphNeighborhoodDepth)
	}
	if options.EdgeTypes == nil {
		options.EdgeTypes = []string{"contains", "declares", "imports", "member-of"}
	}
	options.EdgeTypes = sortedUniqueStrings(options.EdgeTypes)
	for _, edgeType := range options.EdgeTypes {
		if edgeType != "contains" && edgeType != "declares" && edgeType != "imports" && edgeType != "member-of" {
			return options, fmt.Errorf("unsupported code graph edge type %q", edgeType)
		}
	}
	if options.MaximumNodes == 0 {
		options.MaximumNodes = 100
	}
	if options.MaximumNodes < len(options.SeedNodeIDs) || options.MaximumNodes > MaximumCodeGraphNeighborhoodNodes {
		return options, fmt.Errorf("maxNodes must be at least the seed count and at most %d", MaximumCodeGraphNeighborhoodNodes)
	}
	if options.MaximumEdges == 0 {
		options.MaximumEdges = 200
	}
	if options.MaximumEdges < 0 || options.MaximumEdges > MaximumCodeGraphNeighborhoodEdges {
		return options, fmt.Errorf("maxEdges must be between 0 and %d", MaximumCodeGraphNeighborhoodEdges)
	}
	return options, nil
}

func codeGraphNeighborhoodReferences(references []CodeGraphReference, edges []CodeGraphEdge) []CodeGraphReference {
	wanted := make(map[string]bool)
	for _, edge := range edges {
		for _, reference := range edge.References {
			wanted[reference] = true
		}
	}
	result := make([]CodeGraphReference, 0, len(wanted))
	for _, reference := range references {
		if wanted[reference.ID] {
			result = append(result, reference)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func codeGraphAdjacency(ctx context.Context, edges []CodeGraphEdge, edgeTypes map[string]bool, direction string) (map[string][]codeGraphAdjacent, error) {
	adjacency := make(map[string][]codeGraphAdjacent)
	for _, edge := range edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !edgeTypes[edge.Type] {
			continue
		}
		if direction == "outgoing" || direction == "both" {
			adjacency[edge.From] = append(adjacency[edge.From], codeGraphAdjacent{edge: edge, neighbor: edge.To})
		}
		if direction == "incoming" || direction == "both" {
			adjacency[edge.To] = append(adjacency[edge.To], codeGraphAdjacent{edge: edge, neighbor: edge.From})
		}
	}
	for id := range adjacency {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sort.Slice(adjacency[id], func(i, j int) bool {
			if adjacency[id][i].edge.ID != adjacency[id][j].edge.ID {
				return adjacency[id][i].edge.ID < adjacency[id][j].edge.ID
			}
			return adjacency[id][i].neighbor < adjacency[id][j].neighbor
		})
	}
	return adjacency, ctx.Err()
}

func codeGraphDistances(ctx context.Context, seeds []string, adjacency map[string][]codeGraphAdjacent, maximumDepth int) (map[string]int, map[string]codeGraphAdjacent, error) {
	distances := make(map[string]int, len(seeds))
	predecessors := make(map[string]codeGraphAdjacent)
	queue := append([]string(nil), seeds...)
	for _, seed := range seeds {
		distances[seed] = 0
	}
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		current := queue[0]
		queue = queue[1:]
		if distances[current] >= maximumDepth {
			continue
		}
		for _, adjacent := range adjacency[current] {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if _, seen := distances[adjacent.neighbor]; seen {
				continue
			}
			distances[adjacent.neighbor] = distances[current] + 1
			predecessors[adjacent.neighbor] = codeGraphAdjacent{edge: adjacent.edge, neighbor: current}
			queue = append(queue, adjacent.neighbor)
		}
	}
	return distances, predecessors, nil
}

func codeGraphEdgeWithinDirection(edge CodeGraphEdge, distances map[string]int, direction string) bool {
	from, fromOK := distances[edge.From]
	to, toOK := distances[edge.To]
	if !fromOK || !toOK {
		return false
	}
	switch direction {
	case "outgoing":
		return to <= from+1
	case "incoming":
		return from <= to+1
	default:
		return true
	}
}

func sortedUniqueStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	if len(result) == 0 {
		return result
	}
	write := 1
	for _, value := range result[1:] {
		if value != result[write-1] {
			result[write] = value
			write++
		}
	}
	return result[:write]
}
