package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AnalyzeCodeGraphInput struct {
	Root             string   `json:"root,omitempty" jsonschema:"Working directory used to resolve source and coverage paths. Defaults to the MCP server working directory."`
	Paths            []string `json:"paths,omitempty" jsonschema:"C#, Go, Rust, TypeScript, or TSX files and directories to inventory. Defaults to the root directory."`
	CoveragePath     string   `json:"coveragePath,omitempty" jsonschema:"Optional Cobertura XML or Go coverprofile used to decorate callable nodes."`
	CRAPThreshold    *float64 `json:"crapThreshold,omitempty" jsonschema:"Score above which callable nodes are flagged. Defaults to 30."`
	IncludeTests     bool     `json:"includeTests,omitempty" jsonschema:"Include Go _test.go and TypeScript .spec/.test files. Defaults to false."`
	IncludeGenerated bool     `json:"includeGenerated,omitempty" jsonschema:"Include generated source conventions. Defaults to false."`
	Exclude          []string `json:"exclude,omitempty" jsonschema:"Root-relative source exclusion patterns using gitignore syntax."`
	StrictCoverage   bool     `json:"strictCoverage,omitempty" jsonschema:"Fail when coverage paths are unmatched or ambiguous."`
	ResultMode       string   `json:"resultMode,omitempty" jsonschema:"Graph detail to return: summary, nodes, edges, or references. Defaults to summary."`
	Limit            *int     `json:"limit,omitempty" jsonschema:"Maximum node or edge details per response, from 1 through 100. Defaults to 20."`
}

type GetCodeGraphInput struct {
	ReportID   string `json:"reportId,omitempty" jsonschema:"Code graph report ID returned by analyze_code_graph."`
	ResultMode string `json:"resultMode,omitempty" jsonschema:"Graph detail to return: summary, nodes, edges, or references. Defaults to summary."`
	Limit      *int   `json:"limit,omitempty" jsonschema:"Maximum node or edge details per response, from 1 through 100. Defaults to 20."`
	Cursor     string `json:"cursor,omitempty" jsonschema:"Opaque continuation cursor. Do not combine with other fields."`
}

type GetCodeGraphNeighborhoodInput struct {
	ReportID    string   `json:"reportId" jsonschema:"Code graph report ID returned by analyze_code_graph."`
	SeedNodeIDs []string `json:"seedNodeIds" jsonschema:"One through 20 graph node IDs used as traversal seeds."`
	Direction   string   `json:"direction,omitempty" jsonschema:"Traversal direction: incoming, outgoing, or both. Defaults to both."`
	Depth       *int     `json:"depth,omitempty" jsonschema:"Maximum traversal depth, from 0 through 5. Defaults to 1."`
	EdgeTypes   []string `json:"edgeTypes,omitempty" jsonschema:"Edges to traverse: contains, declares, member-of, and imports. Defaults to all."`
	MaxNodes    *int     `json:"maxNodes,omitempty" jsonschema:"Maximum coherent neighborhood nodes, from seed count through 1000. Defaults to 100."`
	MaxEdges    *int     `json:"maxEdges,omitempty" jsonschema:"Maximum neighborhood edges, from 1 through 2000. Defaults to 200."`
}

type CodeGraphOutput struct {
	PageSchemaVersion string                           `json:"pageSchemaVersion"`
	ReportType        string                           `json:"reportType"`
	ReportID          string                           `json:"reportId"`
	ExpiresAt         string                           `json:"expiresAt"`
	SchemaVersion     string                           `json:"schemaVersion"`
	Tool              reportcontract.ToolIdentity      `json:"tool"`
	Fingerprints      reportcontract.Fingerprints      `json:"fingerprints"`
	Coordinates       reportcontract.Coordinates       `json:"coordinates"`
	Grammars          []analysis.GrammarIdentity       `json:"grammars"`
	Coverage          analysis.CoverageMetadata        `json:"coverage"`
	Discovery         analysis.DiscoveryMetadata       `json:"discovery"`
	Threshold         float64                          `json:"threshold"`
	Policy            analysis.CodeGraphPolicy         `json:"policy"`
	Summary           analysis.CodeGraphSummary        `json:"summary"`
	Page              Page                             `json:"page"`
	Nodes             []analysis.CodeGraphNode         `json:"nodes"`
	Edges             []analysis.CodeGraphEdge         `json:"edges"`
	References        []analysis.CodeGraphReference    `json:"references"`
	ResolutionInputs  []reportcontract.FileFingerprint `json:"resolutionInputs"`
	Limitations       []string                         `json:"limitations"`
	Diagnostics       []analysis.Diagnostic            `json:"diagnostics"`
}

type CodeGraphNeighborhoodOutput struct {
	PageSchemaVersion string                         `json:"pageSchemaVersion"`
	ReportID          string                         `json:"reportId"`
	ExpiresAt         string                         `json:"expiresAt"`
	Neighborhood      analysis.CodeGraphNeighborhood `json:"neighborhood"`
}

func registerCodeGraphTools(server *mcp.Server, policy *rootauth.Policy, snapshots *snapshotStore, factory analyzerFactory) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "analyze_code_graph", Title: "Analyze code graph",
		Description: "Build a deterministic graph of logical modules, selected-source dependencies, files, type declarations, callables, and exact lexical containment. It does not infer calls or semantic impact.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest, input AnalyzeCodeGraphInput) (*mcp.CallToolResult, CodeGraphOutput, error) {
		return analyzeCodeGraphWith(ctx, request, input, policy, snapshots, factory)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_code_graph", Title: "Get code graph",
		Description: "Read an immutable page of graph nodes, edges, or import references without rereading source files.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input GetCodeGraphInput) (*mcp.CallToolResult, CodeGraphOutput, error) {
		output, err := getCodeGraph(ctx, snapshots, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_code_graph_neighborhood", Title: "Get code graph neighborhood",
		Description: "Return a coherent bounded structural or module-dependency neighborhood, with exact import evidence, explicit truncation counts, and no dangling edges.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input GetCodeGraphNeighborhoodInput) (*mcp.CallToolResult, CodeGraphNeighborhoodOutput, error) {
		output, err := getCodeGraphNeighborhood(ctx, snapshots, input)
		return nil, output, err
	})
}

func analyzeCodeGraphWith(ctx context.Context, _ *mcp.CallToolRequest, input AnalyzeCodeGraphInput, policy *rootauth.Policy, snapshots *snapshotStore, factory analyzerFactory) (*mcp.CallToolResult, CodeGraphOutput, error) {
	root, err := analysisRoot(input.Root)
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	scope, err := policy.Root(root)
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	threshold, err := analysisThreshold(input.CRAPThreshold)
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	mode, err := codeGraphResultMode(input.ResultMode)
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	limit, err := resultLimit(input.Limit)
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	analyzer, err := factory()
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	defer analyzer.Close()
	report, err := analyzer.AnalyzeCodeGraphContext(ctx, analysis.CodeGraphOptions{
		Root: scope.Path(), Paths: input.Paths, CoveragePath: input.CoveragePath, CRAPThreshold: threshold,
		IncludeTests: input.IncludeTests, IncludeGenerated: input.IncludeGenerated, Exclude: input.Exclude,
		StrictCoverage: input.StrictCoverage, Authorization: scope,
	})
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	output := compactCodeGraph(report, mode, limit, 0)
	item, err := snapshots.putCodeGraphContext(ctx, report)
	if err != nil {
		return nil, CodeGraphOutput{}, err
	}
	return nil, decorateCodeGraphPage(snapshots, output, item, mode, limit, 0), nil
}

func getCodeGraph(ctx context.Context, snapshots *snapshotStore, input GetCodeGraphInput) (CodeGraphOutput, error) {
	mode, reportID, offset, limit := input.ResultMode, input.ReportID, 0, input.Limit
	if input.Cursor != "" {
		if input.ReportID != "" || input.ResultMode != "" || input.Limit != nil {
			return CodeGraphOutput{}, fmt.Errorf("cursor cannot be combined with reportId, resultMode, or limit")
		}
		cursor, err := snapshots.decodeCursor(input.Cursor)
		if err != nil {
			return CodeGraphOutput{}, err
		}
		mode, reportID, offset, limit = cursor.ResultMode, cursor.ReportID, cursor.Offset, &cursor.Limit
	} else if reportID == "" {
		return CodeGraphOutput{}, fmt.Errorf("reportId or cursor is required")
	}
	mode, err := codeGraphResultMode(mode)
	if err != nil {
		return CodeGraphOutput{}, err
	}
	resolvedLimit, err := resultLimit(limit)
	if err != nil {
		return CodeGraphOutput{}, err
	}
	report, item, err := readCodeGraphSnapshot(ctx, snapshots, reportID)
	if err != nil {
		return CodeGraphOutput{}, err
	}
	return decorateCodeGraphPage(snapshots, compactCodeGraph(report, mode, resolvedLimit, offset), item, mode, resolvedLimit, offset), nil
}

func getCodeGraphNeighborhood(ctx context.Context, snapshots *snapshotStore, input GetCodeGraphNeighborhoodInput) (CodeGraphNeighborhoodOutput, error) {
	if input.ReportID == "" {
		return CodeGraphNeighborhoodOutput{}, fmt.Errorf("reportId is required")
	}
	depth, maxNodes, maxEdges := 1, 100, 200
	if input.Depth != nil {
		depth = *input.Depth
	}
	if input.MaxNodes != nil {
		maxNodes = *input.MaxNodes
	}
	if input.MaxEdges != nil {
		maxEdges = *input.MaxEdges
		if maxEdges < 1 {
			return CodeGraphNeighborhoodOutput{}, fmt.Errorf("maxEdges must be between 1 and %d", analysis.MaximumCodeGraphNeighborhoodEdges)
		}
	}
	report, item, err := readCodeGraphSnapshot(ctx, snapshots, input.ReportID)
	if err != nil {
		return CodeGraphNeighborhoodOutput{}, err
	}
	neighborhood, err := analysis.BuildCodeGraphNeighborhoodContext(ctx, report, analysis.CodeGraphNeighborhoodOptions{
		SeedNodeIDs: input.SeedNodeIDs, Direction: input.Direction, Depth: depth, EdgeTypes: input.EdgeTypes,
		MaximumNodes: maxNodes, MaximumEdges: maxEdges,
	})
	if err != nil {
		return CodeGraphNeighborhoodOutput{}, err
	}
	return CodeGraphNeighborhoodOutput{
		PageSchemaVersion: "1", ReportID: item.id, ExpiresAt: item.expiresAt.UTC().Format(time.RFC3339), Neighborhood: neighborhood,
	}, nil
}

func readCodeGraphSnapshot(ctx context.Context, snapshots *snapshotStore, reportID string) (analysis.CodeGraphReport, *snapshot, error) {
	item, err := snapshots.get(reportID)
	if err != nil {
		return analysis.CodeGraphReport{}, nil, err
	}
	select {
	case snapshots.reads <- struct{}{}:
		defer func() { <-snapshots.reads }()
	case <-ctx.Done():
		return analysis.CodeGraphReport{}, nil, ctx.Err()
	}
	report, err := snapshots.decodeCodeGraph(item)
	if err != nil {
		return analysis.CodeGraphReport{}, nil, err
	}
	item, err = snapshots.get(reportID)
	if err != nil {
		return analysis.CodeGraphReport{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return analysis.CodeGraphReport{}, nil, err
	}
	return report, item, nil
}

func compactCodeGraph(report analysis.CodeGraphReport, mode string, limit, offset int) CodeGraphOutput {
	nodes, edges, references := []analysis.CodeGraphNode{}, []analysis.CodeGraphEdge{}, []analysis.CodeGraphReference{}
	total := 0
	if mode == "nodes" {
		total = len(report.Nodes)
		start, end := min(offset, total), min(offset+limit, total)
		nodes = append(nodes, report.Nodes[start:end]...)
	} else if mode == "edges" {
		total = len(report.Edges)
		start, end := min(offset, total), min(offset+limit, total)
		edges = append(edges, report.Edges[start:end]...)
	} else if mode == "references" {
		total = len(report.References)
		start, end := min(offset, total), min(offset+limit, total)
		references = append(references, report.References[start:end]...)
	}
	returned := len(nodes) + len(edges) + len(references)
	return CodeGraphOutput{
		PageSchemaVersion: "1", ReportType: "code-graph-page", SchemaVersion: report.SchemaVersion,
		Tool: report.Tool, Fingerprints: report.Fingerprints, Coordinates: report.Coordinates,
		Grammars: append([]analysis.GrammarIdentity(nil), report.Grammars...), Coverage: report.Coverage,
		Discovery: report.Discovery, Threshold: report.Threshold, Policy: report.Policy, Summary: report.Summary,
		Page:  Page{ResultMode: mode, TotalMatched: total, Offset: offset, Limit: limit, Returned: returned, HasMore: offset+returned < total},
		Nodes: nodes, Edges: edges, References: references, ResolutionInputs: append([]reportcontract.FileFingerprint(nil), report.ResolutionInputs...), Limitations: append([]string(nil), report.Limitations...), Diagnostics: append([]analysis.Diagnostic(nil), report.Diagnostics...),
	}
}

func decorateCodeGraphPage(snapshots *snapshotStore, output CodeGraphOutput, item *snapshot, mode string, limit, offset int) CodeGraphOutput {
	output.ReportID = item.id
	output.ExpiresAt = item.expiresAt.UTC().Format(time.RFC3339)
	if output.Page.HasMore {
		next := offset + output.Page.Returned
		output.Page.NextCursor = snapshots.encodeCursor(cursorState{Version: 1, ReportID: item.id, Offset: next, ResultMode: mode, Limit: limit})
	}
	return output
}

func codeGraphResultMode(mode string) (string, error) {
	if mode == "" {
		return "summary", nil
	}
	if mode != "summary" && mode != "nodes" && mode != "edges" && mode != "references" {
		return "", fmt.Errorf("resultMode must be summary, nodes, edges, or references")
	}
	return mode, nil
}
