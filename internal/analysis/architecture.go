package analysis

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const ArchitectureSchemaVersion = "1"

// ArchitectureRules is a declarative, versioned set of dependency constraints
// evaluated against a code graph. Dependencies are allowed unless a cycle is
// forbidden or a forbid rule matches.
type ArchitectureRules struct {
	SchemaVersion string               `json:"schemaVersion"`
	ForbidCycles  *bool                `json:"forbidCycles,omitempty"`
	Forbid        []ArchitectureForbid `json:"forbid,omitempty"`
}

type ArchitectureForbid struct {
	From   string `json:"from"`
	To     string `json:"to"`
	System string `json:"system,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ArchitectureReport struct {
	SchemaVersion string                  `json:"schemaVersion"`
	ReportType    string                  `json:"reportType"`
	Summary       ArchitectureSummary     `json:"summary"`
	Cycles        []ArchitectureCycle     `json:"cycles"`
	Violations    []ArchitectureViolation `json:"violations"`
	Limitations   []string                `json:"limitations"`
}

type ArchitectureSummary struct {
	Modules    int  `json:"modules"`
	Edges      int  `json:"edges"`
	Cycles     int  `json:"cycles"`
	Violations int  `json:"violations"`
	Complete   bool `json:"complete"`
}

// ArchitectureCycle is a list of module-dependency edges that together form a
// strongly connected component. The edges are a deterministic walk covering
// every member of the component (a directed cycle when the component has more
// than one member, or a self-loop otherwise).
type ArchitectureCycle struct {
	Modules []string                    `json:"modules"`
	Edges   []ArchitectureEdgeReference `json:"edges"`
}

type ArchitectureEdgeReference struct {
	From       string   `json:"from"`
	To         string   `json:"to"`
	References []string `json:"references"`
}

type ArchitectureViolation struct {
	Kind   string                      `json:"kind"`
	System string                      `json:"system"`
	From   string                      `json:"from"`
	To     string                      `json:"to"`
	Reason string                      `json:"reason"`
	Edges  []ArchitectureEdgeReference `json:"edges,omitempty"`
}

// AnalyzeArchitecture evaluates architecture rules and module cycle proofs
// against a code graph. It is a pure function: the same graph, rules, and
// context always yield the same report.
func AnalyzeArchitecture(ctx context.Context, graph CodeGraphReport, rules ArchitectureRules) (ArchitectureReport, error) {
	report := ArchitectureReport{
		SchemaVersion: ArchitectureSchemaVersion,
		ReportType:    "architecture",
		Cycles:        []ArchitectureCycle{},
		Violations:    []ArchitectureViolation{},
		Limitations: []string{
			"module resolution is bounded to selected source and may be incomplete",
			"dependency evidence is static lexical imports and never proves runtime behavior",
		},
	}
	if rules.SchemaVersion != "" && rules.SchemaVersion != ArchitectureSchemaVersion {
		return report, fmt.Errorf("architecture rules schema version %q unsupported", rules.SchemaVersion)
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}

	graphModel := buildArchitectureGraph(graph)
	report.Summary.Modules = len(graphModel.ids)
	report.Summary.Edges = len(graphModel.dependencyEdges)

	components := graphModel.components()
	cycles := make([]ArchitectureCycle, 0, len(components))
	for _, component := range components {
		cycle, ok := graphModel.witnessComponent(component)
		if !ok {
			continue
		}
		cycles = append(cycles, cycle)
	}
	sort.Slice(cycles, func(i, j int) bool {
		return cycles[i].Edges[0].From+"/"+cycles[i].Edges[0].To <
			cycles[j].Edges[0].From+"/"+cycles[j].Edges[0].To
	})
	report.Cycles = cycles

	if rules.shouldForbidCycles() {
		for _, cycle := range cycles {
			first := cycle.Edges[0]
			report.Violations = append(report.Violations, ArchitectureViolation{
				Kind:   "cycle",
				From:   first.From,
				To:     first.To,
				Reason: "import cycle",
				Edges:  append([]ArchitectureEdgeReference(nil), cycle.Edges...),
			})
		}
	}

	for _, edge := range graphModel.dependencyEdges {
		fromName := graphModel.name(edge.From)
		toName := graphModel.name(edge.To)
		fromSystem := graphModel.system(edge.From)
		for _, forbid := range rules.Forbid {
			if forbid.System != "" && forbid.System != fromSystem {
				continue
			}
			if !architectureModuleMatches(forbid.From, fromName) || !architectureModuleMatches(forbid.To, toName) {
				continue
			}
			report.Violations = append(report.Violations, ArchitectureViolation{
				Kind:   "forbid",
				System: fromSystem,
				From:   fromName,
				To:     toName,
				Reason: forbid.Reason,
				Edges: []ArchitectureEdgeReference{{
					From:       fromName,
					To:         toName,
					References: append([]string(nil), edge.References...),
				}},
			})
		}
	}
	sort.Slice(report.Violations, func(i, j int) bool {
		left, right := report.Violations[i], report.Violations[j]
		if left.From != right.From {
			return left.From < right.From
		}
		if left.To != right.To {
			return left.To < right.To
		}
		return left.Kind < right.Kind
	})

	report.Summary.Cycles = len(report.Cycles)
	report.Summary.Violations = len(report.Violations)
	report.Summary.Complete = len(report.Violations) == 0
	return report, ctx.Err()
}

func (rules ArchitectureRules) shouldForbidCycles() bool {
	return rules.ForbidCycles == nil || *rules.ForbidCycles
}

// ---------------------------------------------------------------------------
// Graph model
// ---------------------------------------------------------------------------

type architectureModule struct {
	id     string
	name   string
	system string
	index  int
}

type architectureGraph struct {
	modules         map[string]*architectureModule // module ID -> module
	ids             []string                       // sorted module IDs
	outgoing        map[string][]string            // module ID -> sorted neighbor IDs (dedup)
	dependencyEdges []CodeGraphEdge                // unique imports edges, sorted by ID
	nodeSystemMap   map[string]string              // module ID -> module system
}

func buildArchitectureGraph(graph CodeGraphReport) *architectureGraph {
	result := &architectureGraph{
		modules:       make(map[string]*architectureModule),
		outgoing:      make(map[string][]string),
		nodeSystemMap: make(map[string]string),
	}
	for _, node := range graph.Nodes {
		if node.Kind != "module" || node.Module == nil {
			continue
		}
		key := node.Module.System + "/" + node.Module.Name
		result.modules[node.ID] = &architectureModule{
			id:     node.ID,
			name:   key,
			system: node.Module.System,
			index:  len(result.ids),
		}
		result.ids = append(result.ids, node.ID)
		result.nodeSystemMap[node.ID] = node.Module.System
	}
	sort.Strings(result.ids)

	seenEdges := make(map[string]bool)
	for _, edge := range graph.Edges {
		if edge.Type != "imports" {
			continue
		}
		from, fromOK := result.modules[edge.From]
		_, toOK := result.modules[edge.To]
		if !fromOK || !toOK {
			continue
		}
		_ = from
		key := edge.From + "\x00" + edge.To
		if seenEdges[key] {
			continue
		}
		seenEdges[key] = true
		result.outgoing[edge.From] = append(result.outgoing[edge.From], edge.To)
		result.dependencyEdges = append(result.dependencyEdges, edge)
	}
	for from := range result.outgoing {
		sort.Strings(result.outgoing[from])
	}
	sort.Slice(result.dependencyEdges, func(i, j int) bool {
		return result.dependencyEdges[i].ID < result.dependencyEdges[j].ID
	})
	return result
}

func (g *architectureGraph) name(moduleID string) string {
	if module, ok := g.modules[moduleID]; ok {
		return module.name
	}
	return moduleID
}

func (g *architectureGraph) system(moduleID string) string {
	if module, ok := g.modules[moduleID]; ok {
		return module.system
	}
	return ""
}

// components returns strongly connected components with at least two members,
// each sorted deterministically.
func (g *architectureGraph) components() [][]string {
	indexOf := make(map[string]int)
	lowlink := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var result [][]string
	next := 0

	var strongConnect func(string)
	strongConnect = func(vertex string) {
		indexOf[vertex] = next
		lowlink[vertex] = next
		next++
		stack = append(stack, vertex)
		onStack[vertex] = true

		for _, neighbor := range g.outgoing[vertex] {
			if _, seen := indexOf[neighbor]; !seen {
				strongConnect(neighbor)
				if lowlink[neighbor] < lowlink[vertex] {
					lowlink[vertex] = lowlink[neighbor]
				}
			} else if onStack[neighbor] && indexOf[neighbor] < lowlink[vertex] {
				lowlink[vertex] = indexOf[neighbor]
			}
		}

		if lowlink[vertex] != indexOf[vertex] {
			return
		}
		var component []string
		for {
			popped := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[popped] = false
			component = append(component, popped)
			if popped == vertex {
				break
			}
		}
		sort.Strings(component)
		result = append(result, component)
	}

	for _, id := range g.ids {
		if _, seen := indexOf[id]; !seen {
			strongConnect(id)
		}
	}

	nonTrivial := result[:0]
	for _, component := range result {
		if len(component) > 1 || g.hasSelfLoop(component[0]) {
			nonTrivial = append(nonTrivial, component)
		}
	}
	return nonTrivial
}

func (g *architectureGraph) hasSelfLoop(moduleID string) bool {
	for _, neighbor := range g.outgoing[moduleID] {
		if neighbor == moduleID {
			return true
		}
	}
	return false
}

// witnessComponent builds a cycle witness covering a nontrivial SCC. It repeats
// a deterministic depth-first search over the component's subgraph to find
// directed cycles, peeling one simple cycle off at a time until no directed
// edge remains. Each witness edge records its source modules and the import
// references that justify it. Components passed in must be strongly connected
// and non-trivial, so each iteration makes progress.
func (g *architectureGraph) witnessComponent(component []string) (ArchitectureCycle, bool) {
	adjacency := make(map[string][]string)
	for _, from := range component {
		for _, to := range g.outgoing[from] {
			if containsString(component, to) {
				adjacency[from] = append(adjacency[from], to)
			}
		}
		sort.Strings(adjacency[from])
	}

	cycle := ArchitectureCycle{Modules: append([]string(nil), component...)}
	var witness []ArchitectureEdgeReference

	for {
		start := ""
		for _, id := range component {
			if len(adjacency[id]) > 0 {
				start = id
				break
			}
		}
		if start == "" {
			break
		}

		// Deterministic DFS discovering a back edge; parent tracks the tree
		// so we can reconstruct the ancestor chain for the found back edge.
		const (
			white = 0
			gray  = 1
			black = 2
		)
		color := make(map[string]int, len(component))
		parent := make(map[string]string)
		found := false
		var backFrom, backTo string

		var visit func(string)
		visit = func(node string) {
			if found {
				return
			}
			color[node] = gray
			for _, neighbor := range adjacency[node] {
				if found {
					return
				}
				switch color[neighbor] {
				case gray:
					backFrom, backTo = node, neighbor
					found = true
					return
				case white:
					parent[neighbor] = node
					visit(neighbor)
					if !found {
						color[neighbor] = black
					}
				}
			}
			if !found {
				color[node] = black
			}
		}
		for _, id := range component {
			if color[id] == white && !found {
				visit(id)
			}
		}
		if !found {
			break
		}

		// Reconstruct the fundamental directed cycle. Tree edges are oriented
		// parent -> child, so walking parent links from backFrom to backTo and
		// emitting them in reverse yields real tree edges; the DFS back edge
		// backFrom -> backTo closes the cycle.
		path := []string{backFrom}
		for path[len(path)-1] != backTo {
			next, ok := parent[path[len(path)-1]]
			if !ok || next == "" || next == path[len(path)-1] {
				break
			}
			path = append(path, next)
		}
		if path[len(path)-1] != backTo {
			// Path reconstruction failed; drop out rather than loop forever.
			break
		}

		// path is [backFrom, ..., backTo]; tree edges in order are
		// (path[i+1], path[i]) walked upward, then closing (backFrom, backTo).
		removeEdge := func(from, to string) {
			for j := 0; j < len(adjacency[from]); j++ {
				if adjacency[from][j] == to {
					adjacency[from] = append(adjacency[from][:j], adjacency[from][j+1:]...)
					break
				}
			}
		}
		// Trailing edge into backTo via the last tree step, plus the back edge.
		var ordered [][2]string
		for i := len(path) - 2; i >= 0; i-- {
			ordered = append(ordered, [2]string{path[i+1], path[i]})
		}
		ordered = append(ordered, [2]string{backFrom, backTo})

		before := len(witness)
		for _, pair := range ordered {
			from, to := pair[0], pair[1]
			edge, ok := g.edge(from, to)
			if !ok {
				continue
			}
			witness = append(witness, ArchitectureEdgeReference{
				From:       g.name(from),
				To:         g.name(to),
				References: append([]string(nil), edge.References...),
			})
			removeEdge(from, to)
		}
		if len(witness) == before {
			// No edge removed; progress is impossible. Halt deterministically.
			break
		}
	}

	if len(witness) == 0 {
		return ArchitectureCycle{}, false
	}
	cycle.Edges = witness
	return cycle, true
}

// edge returns the unique dependency edge for the given ordered pair, if any.
func (g *architectureGraph) edge(from, to string) (CodeGraphEdge, bool) {
	for _, edge := range g.dependencyEdges {
		if edge.From == from && edge.To == to {
			return edge, true
		}
	}
	return CodeGraphEdge{}, false
}

// ---------------------------------------------------------------------------
// Globbing
// ---------------------------------------------------------------------------

var architectureGlobCache = struct {
	re   map[string]*regexp.Regexp
	keys []string
}{
	re: make(map[string]*regexp.Regexp),
}

// architectureModuleMatches reports whether name matches the glob pattern, which
// may be empty (match all).
func architectureModuleMatches(pattern, name string) bool {
	if pattern == "" {
		return true
	}
	return architectureGlob(pattern).MatchString(name)
}

// architectureGlob converts a simplified glob pattern into an anchored regexp.
// `*` matches within a path segment, `**` matches across separators, `?`
// matches a single non-separator rune. Results are cached.
func architectureGlob(pattern string) *regexp.Regexp {
	if cached, ok := architectureGlobCache.re[pattern]; ok {
		return cached
	}

	var builder strings.Builder
	builder.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				builder.WriteString(".*")
				i++
			} else {
				builder.WriteString("[^/]*")
			}
		case '?':
			builder.WriteString("[^/]?")
		default:
			builder.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	builder.WriteString("$")

	re := regexp.MustCompile(builder.String())

	// Bound the cache at 256 entries.
	architectureGlobCache.keys = append(architectureGlobCache.keys, pattern)
	if len(architectureGlobCache.keys) > 256 {
		evict := architectureGlobCache.keys[0]
		architectureGlobCache.keys = architectureGlobCache.keys[1:]
		delete(architectureGlobCache.re, evict)
	}
	architectureGlobCache.re[pattern] = re
	return re
}

func containsString(slice []string, target string) bool {
	for _, item := range slice {
		if item == target {
			return true
		}
	}
	return false
}
