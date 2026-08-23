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
	orderedNodeIDs, err := orderedNodeIDsByDistance(ctx, distances)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	selected, requiredEdges, resultNodes, err := selectCodeGraphNeighborhoodNodes(ctx, nodes, orderedNodeIDs, distances, predecessors, resolved.MaximumNodes, resolved.MaximumEdges)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	reachableEdges, optionalEdges, err := selectReachableAndOptionalEdges(ctx, report.Edges, edgeTypes, distances, resolved.Direction, selected, requiredEdges)
	if err != nil {
		return CodeGraphNeighborhood{}, err
	}
	selectedEdges := selectFinalEdges(requiredEdges, optionalEdges, resolved.MaximumEdges)
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

func orderedNodeIDsByDistance(ctx context.Context, distances map[string]int) ([]string, error) {
	ids := make([]string, 0, len(distances))
	for id := range distances {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := distances[ids[i]], distances[ids[j]]
		if left != right {
			return left < right
		}
		return ids[i] < ids[j]
	})
	return ids, nil
}

func selectReachableAndOptionalEdges(ctx context.Context, allEdges []CodeGraphEdge, edgeTypes map[string]bool, distances map[string]int, direction string, selected map[string]bool, requiredEdges map[string]CodeGraphEdge) ([]CodeGraphEdge, []CodeGraphEdge, error) {
	reachable := make([]CodeGraphEdge, 0)
	optional := make([]CodeGraphEdge, 0)
	for _, edge := range allEdges {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if !edgeTypes[edge.Type] || !codeGraphEdgeWithinDirection(edge, distances, direction) {
			continue
		}
		reachable = append(reachable, edge)
		if selected[edge.From] && selected[edge.To] && requiredEdges[edge.ID].ID == "" {
			optional = append(optional, edge)
		}
	}
	sort.Slice(optional, func(i, j int) bool { return optional[i].ID < optional[j].ID })
	return reachable, optional, nil
}

func selectFinalEdges(required map[string]CodeGraphEdge, optional []CodeGraphEdge, maxEdges int) []CodeGraphEdge {
	selected := make([]CodeGraphEdge, 0, maxEdges)
	for _, e := range required {
		selected = append(selected, e)
	}
	rem := maxEdges - len(selected)
	if len(optional) > rem {
		optional = optional[:rem]
	}
	selected = append(selected, optional...)
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected
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
	if err := validateSeedNodeIDs(options.SeedNodeIDs); err != nil {
		return options, err
	}
	options.Direction = normalizeDirection(options.Direction)
	if err := validateDirection(options.Direction); err != nil {
		return options, err
	}
	if err := validateDepth(options.Depth); err != nil {
		return options, err
	}
	if options.EdgeTypes == nil {
		options.EdgeTypes = []string{"contains", "declares", "imports", "member-of"}
	}
	options.EdgeTypes = sortedUniqueStrings(options.EdgeTypes)
	if err := validateEdgeTypes(options.EdgeTypes); err != nil {
		return options, err
	}
	if options.MaximumNodes == 0 {
		options.MaximumNodes = 100
	}
	if err := validateMaxNodes(options.MaximumNodes, len(options.SeedNodeIDs)); err != nil {
		return options, err
	}
	if err := validateMaxEdges(options.MaximumEdges); err != nil {
		return options, err
	}
	return options, nil
}

func validateSeedNodeIDs(ids []string) error {
	if len(ids) == 0 || len(ids) > MaximumCodeGraphNeighborhoodSeeds {
		return fmt.Errorf("seedNodeIds must contain 1 through %d unique IDs", MaximumCodeGraphNeighborhoodSeeds)
	}
	return nil
}

func normalizeDirection(dir string) string {
	if dir == "" {
		return "both"
	}
	return dir
}

func validateDirection(dir string) error {
	if dir != "incoming" && dir != "outgoing" && dir != "both" {
		return fmt.Errorf("direction must be incoming, outgoing, or both")
	}
	return nil
}

func validateDepth(d int) error {
	if d < 0 || d > MaximumCodeGraphNeighborhoodDepth {
		return fmt.Errorf("depth must be between 0 and %d", MaximumCodeGraphNeighborhoodDepth)
	}
	return nil
}

func validateEdgeTypes(types []string) error {
	for _, t := range types {
		if t != "contains" && t != "declares" && t != "imports" && t != "member-of" {
			return fmt.Errorf("unsupported code graph edge type %q", t)
		}
	}
	return nil
}

func validateMaxNodes(n, seedCount int) error {
	if n < seedCount || n > MaximumCodeGraphNeighborhoodNodes {
		return fmt.Errorf("maxNodes must be at least the seed count and at most %d", MaximumCodeGraphNeighborhoodNodes)
	}
	return nil
}

func validateMaxEdges(e int) error {
	if e == 0 {
		e = 200
	}
	if e < 0 || e > MaximumCodeGraphNeighborhoodEdges {
		return fmt.Errorf("maxEdges must be between 0 and %d", MaximumCodeGraphNeighborhoodEdges)
	}
	return nil
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
