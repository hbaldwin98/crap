package mutationmcp

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type RunInput struct {
	Root           string   `json:"root,omitempty" jsonschema:"Authorized project root in which the mutation engine runs. Defaults to the MCP server root."`
	Language       string   `json:"language" jsonschema:"Required language: csharp, go, or typescript."`
	Paths          []string `json:"paths,omitempty" jsonschema:"Production source paths or globs to mutate. Go accepts one package directory; C# and TypeScript accept in-root engine globs."`
	MinimumScore   *float64 `json:"minimumScore,omitempty" jsonschema:"Minimum accepted mutation score from 0 through 100. Defaults to 80."`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"Maximum engine runtime in seconds. Defaults to 1800."`
	Workers        int      `json:"workers,omitempty" jsonschema:"Parallel Gremlins workers for Go. Defaults to 1; maximum resource product with testCpu is 16."`
	TestCPU        int      `json:"testCpu,omitempty" jsonschema:"CPUs per Gremlins test process for Go. Defaults to 1; maximum resource product with workers is 16."`
	Incremental    bool     `json:"incremental,omitempty" jsonschema:"Enable StrykerJS incremental mode. TypeScript only."`
	ReportPath     string   `json:"reportPath,omitempty" jsonschema:"In-root StrykerJS JSON report path when project configuration changes its default location."`
	ResultMode     string   `json:"resultMode,omitempty" jsonschema:"Mutants to return: summary, actionable, or all. Defaults to actionable."`
	Statuses       []string `json:"statuses,omitempty" jsonschema:"Optional normalized statuses overriding the result mode selection."`
	Limit          *int     `json:"limit,omitempty" jsonschema:"Maximum mutants per response, from 1 through 100. Defaults to 20."`
}

type GetResultsInput struct {
	ReportID   string   `json:"reportId,omitempty" jsonschema:"Mutation report ID returned by run_mutation_tests."`
	ResultMode string   `json:"resultMode,omitempty" jsonschema:"Mutants to return: summary, actionable, or all. Defaults to actionable."`
	Statuses   []string `json:"statuses,omitempty" jsonschema:"Optional normalized statuses overriding the result mode selection."`
	Limit      *int     `json:"limit,omitempty" jsonschema:"Maximum mutants per response, from 1 through 100. Defaults to 20."`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"Opaque continuation cursor. Do not combine with other fields."`
}

type InspectInput struct {
	Root           string   `json:"root,omitempty" jsonschema:"Authorized project root. Defaults to the MCP server root."`
	Language       string   `json:"language" jsonschema:"Required language: csharp, go, or typescript."`
	Paths          []string `json:"paths,omitempty" jsonschema:"Production source paths or globs. Go accepts one package directory; C# and TypeScript require in-root patterns."`
	MinimumScore   *float64 `json:"minimumScore,omitempty" jsonschema:"Minimum accepted mutation score from 0 through 100. Defaults to 80."`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"Maximum diagnostic or engine runtime in seconds. Defaults to 1800."`
	Workers        int      `json:"workers,omitempty" jsonschema:"Parallel Gremlins workers for Go. Defaults to 1; maximum resource product with testCpu is 16."`
	TestCPU        int      `json:"testCpu,omitempty" jsonschema:"CPUs per Gremlins test process for Go. Defaults to 1; maximum resource product with workers is 16."`
	Incremental    bool     `json:"incremental,omitempty" jsonschema:"Enable StrykerJS incremental mode. TypeScript only."`
	ReportPath     string   `json:"reportPath,omitempty" jsonschema:"In-root StrykerJS JSON report path."`
}

type MutationOutput struct {
	PageSchemaVersion string                      `json:"pageSchemaVersion"`
	ReportType        string                      `json:"reportType"`
	ReportID          string                      `json:"reportId"`
	ExpiresAt         string                      `json:"expiresAt"`
	SchemaVersion     string                      `json:"schemaVersion"`
	Tool              reportcontract.ToolIdentity `json:"tool"`
	Fingerprints      reportcontract.Fingerprints `json:"fingerprints"`
	Coordinates       reportcontract.Coordinates  `json:"coordinates"`
	Language          string                      `json:"language"`
	Engine            string                      `json:"engine"`
	EngineIdentity    mutation.EngineIdentity     `json:"engineIdentity"`
	Score             *float64                    `json:"score"`
	ScoreSource       string                      `json:"scoreSource"`
	MinimumScore      float64                     `json:"minimumScore"`
	Passed            bool                        `json:"passed"`
	Summary           mutation.Summary            `json:"summary"`
	Provenance        mutation.Provenance         `json:"provenance"`
	Page              MutationPage                `json:"page"`
	Mutants           []mutation.MutantResult     `json:"mutants"`
}

type MutationPage struct {
	ResultMode   string   `json:"resultMode"`
	Statuses     []string `json:"statuses,omitempty"`
	TotalMatched int      `json:"totalMatched"`
	Limit        int      `json:"limit"`
	Returned     int      `json:"returned"`
	HasMore      bool     `json:"hasMore"`
	NextCursor   string   `json:"nextCursor,omitempty"`
}

type executor interface {
	Run(context.Context, mutation.Options, io.Writer) (mutation.Report, error)
}

type inspector interface {
	Plan(mutation.Options) (mutation.Plan, error)
	Doctor(context.Context, mutation.Options) (mutation.DoctorReport, error)
}

func Run(ctx context.Context, version string, policy *rootauth.Policy) error {
	return New(version, policy).Run(ctx, &mcp.StdioTransport{})
}

func New(version string, policy *rootauth.Policy) *mcp.Server {
	service := mutation.NewService()
	return newServerWithInspector(version, service, service, policy, newSnapshotStore())
}

func newServer(version string, service executor, policy *rootauth.Policy, snapshots *snapshotStore) *mcp.Server {
	return newServerWithInspector(version, service, mutation.NewService(), policy, snapshots)
}

func newServerWithInspector(version string, service executor, inspect inspector, policy *rootauth.Policy, snapshots *snapshotStore) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "crap-mutate", Version: version}, &mcp.ServerOptions{
		Instructions: "Use run_mutation_tests when the user asks to run mutation testing for C#, Go, or TypeScript. The native engine must produce every score; never infer mutation scores yourself. Mutation runs execute project code with server privileges and are not sandboxed. Start with actionable results, then use get_mutation_results for later immutable pages.",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_mutation_tests", Title: "Run mutation tests",
		Description: "Execute a native mutation engine inside an authorized root and retain an immutable, pageable normalized report.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false, OpenWorldHint: boolPointer(true), DestructiveHint: boolPointer(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RunInput) (*mcp.CallToolResult, MutationOutput, error) {
		return runMutation(ctx, service, policy, snapshots, input)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "get_mutation_results", Title: "Get mutation results",
		Description: "Read a page from a retained immutable mutation report without rerunning tests.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input GetResultsInput) (*mcp.CallToolResult, MutationOutput, error) {
		output, err := getMutationResults(snapshots, input)
		return nil, output, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "plan_mutation_run", Title: "Plan mutation run",
		Description: "Validate inputs and return the exact native command plan without executing mutation tests.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: boolPointer(false), DestructiveHint: boolPointer(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input InspectInput) (*mcp.CallToolResult, mutation.Plan, error) {
		options, err := inspectOptions(policy, input)
		if err != nil {
			return nil, mutation.Plan{}, err
		}
		plan, err := inspect.Plan(options)
		return nil, plan, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name: "check_mutation_setup", Title: "Check mutation setup",
		Description: "Check the native engine version and project prerequisites without running mutation tests.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, IdempotentHint: false, OpenWorldHint: boolPointer(true), DestructiveHint: boolPointer(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input InspectInput) (*mcp.CallToolResult, mutation.DoctorReport, error) {
		options, err := inspectOptions(policy, input)
		if err != nil {
			return nil, mutation.DoctorReport{}, err
		}
		report, err := inspect.Doctor(ctx, options)
		return nil, report, err
	})
	return server
}

func inspectOptions(policy *rootauth.Policy, input InspectInput) (mutation.Options, error) {
	minimum := 80.0
	if input.MinimumScore != nil {
		minimum = *input.MinimumScore
	}
	if math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum < 0 || minimum > 100 {
		return mutation.Options{}, fmt.Errorf("minimumScore must be between 0 and 100")
	}
	scope, err := policy.Root(input.Root)
	if err != nil {
		return mutation.Options{}, err
	}
	return mutation.Options{
		Root: scope.Path(), Language: input.Language, Paths: input.Paths, MinimumScore: minimum,
		TimeoutSeconds: input.TimeoutSeconds, Workers: input.Workers, TestCPU: input.TestCPU,
		Incremental: input.Incremental, ReportPath: input.ReportPath, Authorization: scope,
	}, nil
}

func runMutation(ctx context.Context, service executor, policy *rootauth.Policy, snapshots *snapshotStore, input RunInput) (*mcp.CallToolResult, MutationOutput, error) {
	minimum := 80.0
	if input.MinimumScore != nil {
		minimum = *input.MinimumScore
	}
	if math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum < 0 || minimum > 100 {
		return nil, MutationOutput{}, fmt.Errorf("minimumScore must be between 0 and 100")
	}
	scope, err := policy.Root(input.Root)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	query, err := normalizeQuery(input.ResultMode, input.Statuses, input.Limit)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	report, err := service.Run(ctx, mutation.Options{
		Root: scope.Path(), Language: input.Language, Paths: input.Paths, MinimumScore: minimum,
		TimeoutSeconds: input.TimeoutSeconds, Workers: input.Workers, TestCPU: input.TestCPU,
		Incremental: input.Incremental, ReportPath: input.ReportPath, Authorization: scope,
	}, nil)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	item, err := snapshots.put(report)
	if err != nil {
		return nil, MutationOutput{}, err
	}
	return nil, pageReport(snapshots, report, item, query, 0), nil
}

type resultQuery struct {
	mode     string
	statuses []string
	limit    int
}

func normalizeQuery(mode string, statuses []string, requestedLimit *int) (resultQuery, error) {
	mode, err := normalizeQueryMode(mode)
	if err != nil {
		return resultQuery{}, err
	}
	limit := 20
	if requestedLimit != nil {
		limit = *requestedLimit
	}
	if limit < 1 || limit > 100 {
		return resultQuery{}, fmt.Errorf("limit must be between 1 and 100")
	}
	statuses, err = normalizeQueryStatuses(mode, statuses)
	if err != nil {
		return resultQuery{}, err
	}
	return resultQuery{mode: mode, statuses: statuses, limit: limit}, nil
}

func normalizeQueryMode(mode string) (string, error) {
	if mode == "" {
		mode = "actionable"
	}
	if mode != "summary" && mode != "actionable" && mode != "all" {
		return "", fmt.Errorf("resultMode must be summary, actionable, or all")
	}
	return mode, nil
}

func normalizeQueryStatuses(mode string, statuses []string) ([]string, error) {
	if mode == "summary" && len(statuses) > 0 {
		return nil, fmt.Errorf("statuses cannot be used with summary resultMode")
	}
	allowed := map[string]bool{"killed": true, "survived": true, "timedOut": true, "noCoverage": true, "compileError": true, "runtimeError": true, "ignored": true}
	unique := make(map[string]bool)
	for _, status := range statuses {
		if !allowed[status] {
			return nil, fmt.Errorf("unsupported mutation status %q", status)
		}
		unique[status] = true
	}
	statuses = statuses[:0]
	for status := range unique {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	if len(statuses) == 0 && mode == "actionable" {
		statuses = []string{"noCoverage", "survived"}
	}
	return statuses, nil
}

func getMutationResults(snapshots *snapshotStore, input GetResultsInput) (MutationOutput, error) {
	var query resultQuery
	var reportID string
	offset := 0
	if input.Cursor != "" {
		if input.ReportID != "" || input.ResultMode != "" || len(input.Statuses) > 0 || input.Limit != nil {
			return MutationOutput{}, fmt.Errorf("cursor cannot be combined with reportId, resultMode, statuses, or limit")
		}
		cursor, err := snapshots.decodeCursor(input.Cursor)
		if err != nil {
			return MutationOutput{}, err
		}
		reportID, offset = cursor.ReportID, cursor.Offset
		query, err = normalizeQuery(cursor.ResultMode, cursor.Statuses, &cursor.Limit)
		if err != nil {
			return MutationOutput{}, fmt.Errorf("invalid mutation cursor")
		}
	} else {
		if input.ReportID == "" {
			return MutationOutput{}, fmt.Errorf("reportId or cursor is required")
		}
		var err error
		query, err = normalizeQuery(input.ResultMode, input.Statuses, input.Limit)
		if err != nil {
			return MutationOutput{}, err
		}
		reportID = input.ReportID
	}
	item, err := snapshots.get(reportID)
	if err != nil {
		return MutationOutput{}, err
	}
	snapshots.reads <- struct{}{}
	defer func() { <-snapshots.reads }()
	report, err := snapshots.decode(item)
	if err != nil {
		return MutationOutput{}, err
	}
	return pageReport(snapshots, report, item, query, offset), nil
}

func pageReport(snapshots *snapshotStore, report mutation.Report, item *snapshot, query resultQuery, offset int) MutationOutput {
	if report.Fingerprints.Sources == nil {
		report.Fingerprints.Sources = []reportcontract.FileFingerprint{}
	}
	statusSet := make(map[string]bool, len(query.statuses))
	for _, status := range query.statuses {
		statusSet[status] = true
	}
	mutants := make([]mutation.MutantResult, 0, query.limit)
	total := 0
	if query.mode != "summary" {
		for _, mutant := range report.Mutants {
			matches := query.mode == "all" && len(statusSet) == 0 || statusSet[mutant.Status]
			if !matches {
				continue
			}
			if total >= offset && len(mutants) < query.limit {
				mutants = append(mutants, mutant)
			}
			total++
		}
	}
	if mutants == nil {
		mutants = []mutation.MutantResult{}
	}
	nextOffset := offset + len(mutants)
	page := MutationPage{ResultMode: query.mode, Statuses: append([]string(nil), query.statuses...), TotalMatched: total, Limit: query.limit, Returned: len(mutants), HasMore: nextOffset < total}
	if page.HasMore {
		page.NextCursor = snapshots.encodeCursor(cursorState{Version: 1, ReportID: item.id, Offset: nextOffset, ResultMode: query.mode, Statuses: query.statuses, Limit: query.limit})
	}
	return MutationOutput{
		PageSchemaVersion: "2", ReportType: "mutation-page", ReportID: item.id, ExpiresAt: item.expiresAt.UTC().Format(time.RFC3339), SchemaVersion: report.SchemaVersion,
		Tool: report.Tool, Fingerprints: report.Fingerprints, Coordinates: report.Coordinates,
		Language: report.Language, Engine: report.Engine, EngineIdentity: report.EngineIdentity, Score: report.Score, ScoreSource: report.ScoreSource,
		MinimumScore: report.MinimumScore, Passed: report.Passed, Summary: report.Summary, Provenance: report.Provenance,
		Page: page, Mutants: mutants,
	}
}

func boolPointer(value bool) *bool { return &value }
