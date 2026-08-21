package mcpserver

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AnalyzeInput struct {
	Root             string   `json:"root,omitempty" jsonschema:"Working directory used to resolve paths, coverage, and Git revisions. Defaults to the MCP server working directory."`
	Paths            []string `json:"paths,omitempty" jsonschema:"C#, Go, TypeScript, or TSX files and directories to analyze, relative to root. Defaults to the root directory."`
	CoveragePath     string   `json:"coveragePath,omitempty" jsonschema:"Cobertura XML or Go coverprofile path, relative to root. Omit to score with unknown coverage treated as zero."`
	DiffBase         string   `json:"diffBase,omitempty" jsonschema:"Git revision whose merge base with HEAD defines changed mode. Returns callables intersecting added, modified, or deletion-anchored current lines."`
	CRAPThreshold    *float64 `json:"crapThreshold,omitempty" jsonschema:"Score above which a callable is flagged. Defaults to 30."`
	IncludeTests     bool     `json:"includeTests,omitempty" jsonschema:"Include Go _test.go and TypeScript .spec/.test files. Defaults to false."`
	IncludeGenerated bool     `json:"includeGenerated,omitempty" jsonschema:"Include generated source conventions such as .g.cs, .designer.cs, .d.ts, and .generated.ts. Defaults to false."`
	Exclude          []string `json:"exclude,omitempty" jsonschema:"Root-relative source exclusion patterns using gitignore syntax. Negated patterns are not accepted."`
	StrictCoverage   bool     `json:"strictCoverage,omitempty" jsonschema:"Fail when a supplied coverage report has an unmatched or ambiguous analyzed source path."`
	ResultMode       string   `json:"resultMode,omitempty" jsonschema:"Method detail to return: summary, violations, highest, or all. Defaults to violations."`
	Limit            *int     `json:"limit,omitempty" jsonschema:"Maximum method details per response, from 1 through 100. Defaults to 20."`
	Offset           int      `json:"offset,omitempty" jsonschema:"Deprecated zero-based initial offset. Prefer get_analysis_results continuation cursors."`
}

type GetResultsInput struct {
	ReportID   string `json:"reportId,omitempty" jsonschema:"Analysis report ID returned by analyze_code."`
	ResultMode string `json:"resultMode,omitempty" jsonschema:"Method detail to return: summary, violations, highest, or all. Defaults to violations."`
	Limit      *int   `json:"limit,omitempty" jsonschema:"Maximum method details per response, from 1 through 100. Defaults to 20."`
	Cursor     string `json:"cursor,omitempty" jsonschema:"Opaque continuation cursor. Do not combine with other fields."`
}

type AnalyzeOutput struct {
	PageSchemaVersion string                      `json:"pageSchemaVersion"`
	ReportType        string                      `json:"reportType"`
	ReportID          string                      `json:"reportId"`
	ExpiresAt         string                      `json:"expiresAt"`
	SchemaVersion     string                      `json:"schemaVersion"`
	Tool              reportcontract.ToolIdentity `json:"tool"`
	Fingerprints      reportcontract.Fingerprints `json:"fingerprints"`
	Coordinates       reportcontract.Coordinates  `json:"coordinates"`
	Grammars          []analysis.GrammarIdentity  `json:"grammars"`
	Mode              string                      `json:"mode"`
	Coverage          analysis.CoverageMetadata   `json:"coverage"`
	Discovery         analysis.DiscoveryMetadata  `json:"discovery"`
	DiffBase          string                      `json:"diffBase,omitempty"`
	DiffBaseCommit    string                      `json:"diffBaseCommit,omitempty"`
	DiffHeadCommit    string                      `json:"diffHeadCommit,omitempty"`
	DiffMergeBase     string                      `json:"diffMergeBase,omitempty"`
	Threshold         float64                     `json:"threshold"`
	Summary           analysis.Summary            `json:"summary"`
	Page              Page                        `json:"page"`
	Methods           []analysis.MethodResult     `json:"methods"`
	Diagnostics       []analysis.Diagnostic       `json:"diagnostics"`
}

type Page struct {
	ResultMode   string `json:"resultMode"`
	TotalMatched int    `json:"totalMatched"`
	Offset       int    `json:"offset"`
	Limit        int    `json:"limit"`
	Returned     int    `json:"returned"`
	HasMore      bool   `json:"hasMore"`
	NextCursor   string `json:"nextCursor,omitempty"`
	NextOffset   *int   `json:"nextOffset,omitempty"`
}

type analyzerExecution interface {
	AnalyzeContext(context.Context, analysis.Options) (analysis.Report, error)
	Close()
}

type analyzerFactory func() (analyzerExecution, error)

func Run(ctx context.Context, version string, policy *rootauth.Policy) error {
	return New(version, policy).Run(ctx, &mcp.StdioTransport{})
}

func New(version string, policy *rootauth.Policy) *mcp.Server {
	return newServer(version, policy, newSnapshotStore(), func() (analyzerExecution, error) { return analysis.NewAnalyzer() })
}

func newServer(version string, policy *rootauth.Policy, snapshots *snapshotStore, factory analyzerFactory) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "crap", Version: version}, &mcp.ServerOptions{
		Instructions: "Use analyze_code whenever the user asks to run, check, or report CRAP scores or cyclomatic complexity for C#, Go, or TypeScript. Never estimate these scores yourself. Start with the default violations view or resultMode=summary, then use get_analysis_results for later immutable pages.",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze_code",
		Title:       "Analyze CRAP scores",
		Description: "Run this tool when asked for CRAP scores or cyclomatic complexity. It deterministically analyzes C#, Go, and TypeScript and returns compact, pageable method details; scores must not be inferred by the caller.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest, input AnalyzeInput) (*mcp.CallToolResult, AnalyzeOutput, error) {
		return analyzeWith(ctx, request, input, policy, snapshots, factory)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_analysis_results", Title: "Get analysis results",
		Description: "Read a page from a retained immutable analysis report without rerunning analysis.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input GetResultsInput) (*mcp.CallToolResult, AnalyzeOutput, error) {
		output, err := getAnalysisResults(ctx, snapshots, input)
		return nil, output, err
	})
	return server
}

func analyze(ctx context.Context, request *mcp.CallToolRequest, input AnalyzeInput, policy *rootauth.Policy) (*mcp.CallToolResult, AnalyzeOutput, error) {
	return analyzeWith(ctx, request, input, policy, newSnapshotStore(), func() (analyzerExecution, error) { return analysis.NewAnalyzer() })
}

func analyzeWith(ctx context.Context, _ *mcp.CallToolRequest, input AnalyzeInput, policy *rootauth.Policy, snapshots *snapshotStore, factory analyzerFactory) (*mcp.CallToolResult, AnalyzeOutput, error) {
	if input.Offset < 0 {
		return nil, AnalyzeOutput{}, fmt.Errorf("offset must not be negative")
	}
	root, err := analysisRoot(input.Root)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	scope, err := policy.Root(root)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	root = scope.Path()
	threshold, err := analysisThreshold(input.CRAPThreshold)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	mode, err := resultMode(input.ResultMode)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	limit, err := resultLimit(input.Limit)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	analyzer, err := factory()
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	defer analyzer.Close()
	report, err := analyzer.AnalyzeContext(ctx, analysis.Options{
		Root:             root,
		Paths:            input.Paths,
		CoveragePath:     input.CoveragePath,
		DiffBase:         input.DiffBase,
		CRAPThreshold:    threshold,
		IncludeTests:     input.IncludeTests,
		IncludeGenerated: input.IncludeGenerated,
		Exclude:          input.Exclude,
		StrictCoverage:   input.StrictCoverage,
		Authorization:    scope,
	})
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, AnalyzeOutput{}, err
	}
	output, err := compactReportContext(ctx, report, mode, limit, input.Offset)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	item, err := snapshots.putContext(ctx, report)
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	return nil, decoratePage(snapshots, output, item, mode, limit, input.Offset), nil
}

func getAnalysisResults(ctx context.Context, snapshots *snapshotStore, input GetResultsInput) (AnalyzeOutput, error) {
	mode, reportID, offset, limit := input.ResultMode, input.ReportID, 0, input.Limit
	if input.Cursor != "" {
		if input.ReportID != "" || input.ResultMode != "" || input.Limit != nil {
			return AnalyzeOutput{}, fmt.Errorf("cursor cannot be combined with reportId, resultMode, or limit")
		}
		cursor, err := snapshots.decodeCursor(input.Cursor)
		if err != nil {
			return AnalyzeOutput{}, err
		}
		mode, reportID, offset, limit = cursor.ResultMode, cursor.ReportID, cursor.Offset, &cursor.Limit
	} else if reportID == "" {
		return AnalyzeOutput{}, fmt.Errorf("reportId or cursor is required")
	}
	mode, err := resultMode(mode)
	if err != nil {
		return AnalyzeOutput{}, err
	}
	resolvedLimit, err := resultLimit(limit)
	if err != nil {
		return AnalyzeOutput{}, err
	}
	item, err := snapshots.get(reportID)
	if err != nil {
		return AnalyzeOutput{}, err
	}
	select {
	case snapshots.reads <- struct{}{}:
		defer func() { <-snapshots.reads }()
	case <-ctx.Done():
		return AnalyzeOutput{}, ctx.Err()
	}
	report, err := snapshots.decode(item)
	if err != nil {
		return AnalyzeOutput{}, err
	}
	if err := ctx.Err(); err != nil {
		return AnalyzeOutput{}, err
	}
	output, err := compactReportContext(ctx, report, mode, resolvedLimit, offset)
	if err != nil {
		return AnalyzeOutput{}, err
	}
	return decoratePage(snapshots, output, item, mode, resolvedLimit, offset), nil
}

func decoratePage(snapshots *snapshotStore, output AnalyzeOutput, item *snapshot, mode string, limit, offset int) AnalyzeOutput {
	output.PageSchemaVersion = "4"
	output.ReportID = item.id
	output.ExpiresAt = item.expiresAt.UTC().Format(time.RFC3339)
	if output.Page.HasMore {
		next := offset + output.Page.Returned
		output.Page.NextCursor = snapshots.encodeCursor(cursorState{Version: 1, ReportID: item.id, Offset: next, ResultMode: mode, Limit: limit})
		output.Page.NextOffset = &next
	}
	return output
}

func analysisRoot(root string) (string, error) {
	return root, nil
}

func analysisThreshold(value *float64) (float64, error) {
	if value == nil {
		return 30, nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return 0, fmt.Errorf("crapThreshold must not be negative")
	}
	return *value, nil
}

func boolPointer(value bool) *bool { return &value }

func resultMode(mode string) (string, error) {
	if mode == "" {
		return "violations", nil
	}
	if mode != "summary" && mode != "violations" && mode != "highest" && mode != "all" {
		return "", fmt.Errorf("resultMode must be summary, violations, highest, or all")
	}
	return mode, nil
}

func resultLimit(value *int) (int, error) {
	if value == nil {
		return 20, nil
	}
	if *value < 1 || *value > 100 {
		return 0, fmt.Errorf("limit must be between 1 and 100")
	}
	return *value, nil
}

func compactReport(report analysis.Report, mode string, limit, offset int) AnalyzeOutput {
	output, _ := compactReportContext(context.Background(), report, mode, limit, offset)
	return output
}

func compactReportContext(ctx context.Context, report analysis.Report, mode string, limit, offset int) (AnalyzeOutput, error) {
	if report.Fingerprints.Sources == nil {
		report.Fingerprints.Sources = []reportcontract.FileFingerprint{}
	}
	methods := make([]analysis.MethodResult, 0, len(report.Methods))
	for _, method := range report.Methods {
		if err := ctx.Err(); err != nil {
			return AnalyzeOutput{}, err
		}
		if mode == "summary" {
			break
		}
		if mode != "violations" || method.AboveThreshold {
			methods = append(methods, method)
		}
	}
	sort.SliceStable(methods, func(i, j int) bool {
		if methods[i].CRAP != methods[j].CRAP {
			return methods[i].CRAP > methods[j].CRAP
		}
		return methods[i].ID < methods[j].ID
	})
	if err := ctx.Err(); err != nil {
		return AnalyzeOutput{}, err
	}
	total := len(methods)
	start := min(offset, total)
	end := min(start+limit, total)
	pageMethods := append([]analysis.MethodResult(nil), methods[start:end]...)
	if pageMethods == nil {
		pageMethods = []analysis.MethodResult{}
	}
	page := Page{ResultMode: mode, TotalMatched: total, Offset: offset, Limit: limit, Returned: len(pageMethods), HasMore: end < total}
	return AnalyzeOutput{
		PageSchemaVersion: "3", ReportType: "analysis-page", SchemaVersion: report.SchemaVersion,
		Tool: report.Tool, Fingerprints: report.Fingerprints, Coordinates: report.Coordinates, Grammars: append(make([]analysis.GrammarIdentity, 0, len(report.Grammars)), report.Grammars...),
		Mode: report.Mode, Coverage: report.Coverage, Discovery: report.Discovery,
		DiffBase: report.DiffBase, DiffBaseCommit: report.DiffBaseCommit,
		DiffHeadCommit: report.DiffHeadCommit, DiffMergeBase: report.DiffMergeBase,
		Threshold: report.Threshold, Summary: report.Summary,
		Page: page, Methods: pageMethods, Diagnostics: append(make([]analysis.Diagnostic, 0, len(report.Diagnostics)), report.Diagnostics...),
	}, nil
}
