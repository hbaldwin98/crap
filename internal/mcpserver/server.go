package mcpserver

import (
	"context"
	"fmt"
	"math"
	"sort"

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
	Offset           int      `json:"offset,omitempty" jsonschema:"Zero-based offset into matching method details. Defaults to 0."`
}

type AnalyzeOutput struct {
	PageSchemaVersion string                      `json:"pageSchemaVersion"`
	ReportType        string                      `json:"reportType"`
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
	NextOffset   *int   `json:"nextOffset,omitempty"`
}

func Run(ctx context.Context, version string, policy *rootauth.Policy) error {
	return New(version, policy).Run(ctx, &mcp.StdioTransport{})
}

func New(version string, policy *rootauth.Policy) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "crap", Version: version}, &mcp.ServerOptions{
		Instructions: "Use analyze_code whenever the user asks to run, check, or report CRAP scores or cyclomatic complexity for C#, Go, or TypeScript. Never estimate these scores yourself. Start with the default violations view or resultMode=summary. Request highest/all pages or narrower paths only when details are needed.",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze_code",
		Title:       "Analyze CRAP scores",
		Description: "Run this tool when asked for CRAP scores or cyclomatic complexity. It deterministically analyzes C#, Go, and TypeScript and returns compact, pageable method details; scores must not be inferred by the caller.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest, input AnalyzeInput) (*mcp.CallToolResult, AnalyzeOutput, error) {
		return analyze(ctx, request, input, policy)
	})
	return server
}

func analyze(_ context.Context, _ *mcp.CallToolRequest, input AnalyzeInput, policy *rootauth.Policy) (*mcp.CallToolResult, AnalyzeOutput, error) {
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
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		return nil, AnalyzeOutput{}, err
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(analysis.Options{
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
	return nil, compactReport(report, mode, limit, input.Offset), nil
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
	if report.Fingerprints.Sources == nil {
		report.Fingerprints.Sources = []reportcontract.FileFingerprint{}
	}
	methods := make([]analysis.MethodResult, 0, len(report.Methods))
	for _, method := range report.Methods {
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
	total := len(methods)
	start := min(offset, total)
	end := min(start+limit, total)
	pageMethods := append([]analysis.MethodResult(nil), methods[start:end]...)
	if pageMethods == nil {
		pageMethods = []analysis.MethodResult{}
	}
	page := Page{ResultMode: mode, TotalMatched: total, Offset: offset, Limit: limit, Returned: len(pageMethods), HasMore: end < total}
	if page.HasMore {
		next := end
		page.NextOffset = &next
	}
	return AnalyzeOutput{
		PageSchemaVersion: "3", ReportType: "analysis-page", SchemaVersion: report.SchemaVersion,
		Tool: report.Tool, Fingerprints: report.Fingerprints, Coordinates: report.Coordinates, Grammars: append(make([]analysis.GrammarIdentity, 0, len(report.Grammars)), report.Grammars...),
		Mode: report.Mode, Coverage: report.Coverage, Discovery: report.Discovery,
		DiffBase: report.DiffBase, DiffBaseCommit: report.DiffBaseCommit,
		DiffHeadCommit: report.DiffHeadCommit, DiffMergeBase: report.DiffMergeBase,
		Threshold: report.Threshold, Summary: report.Summary,
		Page: page, Methods: pageMethods, Diagnostics: append(make([]analysis.Diagnostic, 0, len(report.Diagnostics)), report.Diagnostics...),
	}
}
