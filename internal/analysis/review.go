package analysis

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hbaldwin98/crap/internal/reportcontract"
)

const (
	ReviewSchemaVersion           = "1"
	ReviewLowCoveragePercent      = 50
	reviewFindingRegressionRank   = 10
	reviewFindingQualityRank      = 20
	reviewFindingCoverageRank     = 25
	reviewFindingTestRank         = 30
	reviewFindingArchitectureRank = 40
)

type ReviewOptions struct {
	Analysis          Options
	BaseRevision      string
	BaseCoveragePath  string
	ArchitectureRules ArchitectureRules
	IncludeCallGraph  bool
}

type ReviewReport struct {
	SchemaVersion string                      `json:"schemaVersion"`
	ReportType    string                      `json:"reportType"`
	Tool          reportcontract.ToolIdentity `json:"tool"`
	Coordinates   reportcontract.Coordinates  `json:"coordinates"`
	Grammars      []GrammarIdentity           `json:"grammars"`
	BaseRevision  string                      `json:"baseRevision"`
	BaseCommit    string                      `json:"baseCommit"`
	HeadCommit    string                      `json:"headCommit"`
	MergeBase     string                      `json:"mergeBase"`
	Threshold     float64                     `json:"threshold"`
	Summary       ReviewSummary               `json:"summary"`
	Evidence      ReviewEvidence              `json:"evidence"`
	Findings      []ReviewFinding             `json:"findings"`
	Limitations   []string                    `json:"limitations"`
	Diagnostics   []ReviewDiagnostic          `json:"diagnostics"`
}

type ReviewSummary struct {
	ChangedFiles           int  `json:"changedFiles"`
	ChangedCallables       int  `json:"changedCallables"`
	NewRegressions         int  `json:"newRegressions"`
	QualityFindings        int  `json:"qualityFindings"`
	AffectedTests          int  `json:"affectedTests"`
	ArchitectureViolations int  `json:"architectureViolations"`
	ArchitectureCycles     int  `json:"architectureCycles"`
	Limitations            int  `json:"limitations"`
	Diagnostics            int  `json:"diagnostics"`
	Complete               bool `json:"complete"`
}

type ReviewEvidence struct {
	Scope                  ChangeScopeSummary      `json:"scope"`
	Quality                ReviewQualityEvidence   `json:"quality"`
	Comparison             ComparisonSummary       `json:"comparison"`
	Regressions            []CallableComparison    `json:"regressions"`
	Architecture           ArchitectureSummary     `json:"architecture"`
	ArchitectureCycles     []ArchitectureCycle     `json:"architectureCycles"`
	ArchitectureViolations []ArchitectureViolation `json:"architectureViolations"`
	CallGraph              *CallGraphSummary       `json:"callGraph,omitempty"`
	AffectedTests          []CallGraphAffectedTest `json:"affectedTests"`
}

type ReviewQualityEvidence struct {
	Coverage         CoverageMetadata `json:"coverage"`
	CurrentGraph     CodeGraphSummary `json:"currentGraph"`
	ChangedCallables []MethodResult   `json:"changedCallables"`
}

type ReviewFinding struct {
	ID       string                `json:"id"`
	Kind     string                `json:"kind"`
	Priority int                   `json:"priority"`
	Severity string                `json:"severity"`
	Title    string                `json:"title"`
	Evidence ReviewFindingEvidence `json:"evidence"`
}

type ReviewFindingEvidence struct {
	ComparisonID    string   `json:"comparisonId,omitempty"`
	CallableID      string   `json:"callableId,omitempty"`
	File            string   `json:"file,omitempty"`
	Name            string   `json:"name,omitempty"`
	StartLine       int      `json:"startLine,omitempty"`
	CRAP            *float64 `json:"crap,omitempty"`
	CoveragePercent *float64 `json:"coveragePercent,omitempty"`
	Reasons         []string `json:"reasons,omitempty"`
	Test            string   `json:"test,omitempty"`
	TestFile        string   `json:"testFile,omitempty"`
	Distance        int      `json:"distance,omitempty"`
	From            string   `json:"from,omitempty"`
	To              string   `json:"to,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Modules         []string `json:"modules,omitempty"`
}

type ReviewDiagnostic struct {
	Source     string     `json:"source"`
	Diagnostic Diagnostic `json:"diagnostic"`
}

func (analyzer *Analyzer) AnalyzeReview(options ReviewOptions) (ReviewReport, error) {
	return analyzer.AnalyzeReviewContext(context.Background(), options)
}

func (analyzer *Analyzer) AnalyzeReviewContext(ctx context.Context, options ReviewOptions) (ReviewReport, error) {
	if err := validateReviewOptions(options); err != nil {
		return ReviewReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReviewReport{}, err
	}

	scopeOptions := options.Analysis
	scopeOptions.DiffBase = options.BaseRevision
	scope, err := analyzer.AnalyzeChangeScopeContext(ctx, scopeOptions)
	if err != nil {
		return ReviewReport{}, err
	}
	comparison, err := analyzer.CompareChangeScopeContext(ctx, ComparisonOptions{
		Analysis: options.Analysis, BaseRevision: options.BaseRevision, BaseCoveragePath: options.BaseCoveragePath,
	})
	if err != nil {
		return ReviewReport{}, err
	}
	graph, err := analyzer.AnalyzeCodeGraphContext(ctx, CodeGraphOptions{
		Paths: options.Analysis.Paths, CoveragePath: options.Analysis.CoveragePath, Root: options.Analysis.Root,
		CRAPThreshold: options.Analysis.CRAPThreshold, IncludeTests: options.Analysis.IncludeTests,
		IncludeGenerated: options.Analysis.IncludeGenerated, Exclude: options.Analysis.Exclude,
		StrictCoverage: options.Analysis.StrictCoverage, Authorization: options.Analysis.Authorization,
	})
	if err != nil {
		return ReviewReport{}, err
	}
	architecture, err := AnalyzeArchitecture(ctx, graph, options.ArchitectureRules)
	if err != nil {
		return ReviewReport{}, err
	}

	var callGraph *CallGraphReport
	if options.IncludeCallGraph {
		result, err := analyzer.AnalyzeCallGraphContext(ctx, CallGraphOptions{
			Paths: options.Analysis.Paths, Root: options.Analysis.Root, DiffBase: options.BaseRevision,
			IncludeGenerated: options.Analysis.IncludeGenerated, Exclude: options.Analysis.Exclude,
			Authorization: options.Analysis.Authorization,
		})
		if err != nil {
			return ReviewReport{}, err
		}
		callGraph = &result
	}
	return buildReviewReport(options, scope, comparison, graph, architecture, callGraph), nil
}

func validateReviewOptions(options ReviewOptions) error {
	if strings.TrimSpace(options.BaseRevision) == "" {
		return fmt.Errorf("base revision is required")
	}
	if options.Analysis.DiffBase != "" {
		return fmt.Errorf("review does not accept an analysis diff base")
	}
	return nil
}

func buildReviewReport(options ReviewOptions, scope ChangeScopeReport, comparison ChangeScopeComparisonReport, graph CodeGraphReport, architecture ArchitectureReport, callGraph *CallGraphReport) ReviewReport {
	report := ReviewReport{
		SchemaVersion: ReviewSchemaVersion, ReportType: "review", Tool: graph.Tool, Coordinates: graph.Coordinates,
		Grammars:     reviewGrammars(scope.Grammars, comparison.Grammars, graph.Grammars),
		BaseRevision: options.BaseRevision, BaseCommit: comparison.BaseCommit, HeadCommit: comparison.HeadCommit,
		MergeBase: comparison.MergeBase, Threshold: options.Analysis.CRAPThreshold,
		Evidence: ReviewEvidence{
			Scope: scope.Summary, Quality: ReviewQualityEvidence{
				Coverage: graph.Coverage, CurrentGraph: graph.Summary,
				ChangedCallables: append([]MethodResult(nil), scope.Callables...),
			},
			Comparison: comparison.Summary, Regressions: make([]CallableComparison, 0),
			Architecture: architecture.Summary, ArchitectureCycles: append([]ArchitectureCycle{}, architecture.Cycles...),
			ArchitectureViolations: append([]ArchitectureViolation{}, architecture.Violations...),
			AffectedTests:          []CallGraphAffectedTest{},
		},
		Findings: make([]ReviewFinding, 0), Limitations: make([]string, 0), Diagnostics: make([]ReviewDiagnostic, 0),
	}
	if callGraph != nil {
		report.Evidence.CallGraph = &callGraph.Summary
		report.Evidence.AffectedTests = append(report.Evidence.AffectedTests, callGraph.AffectedTests...)
		report.Grammars = reviewGrammars(report.Grammars, callGraph.Grammars)
	}
	for _, comparison := range comparison.Callables {
		if comparison.NewRegression {
			report.Evidence.Regressions = append(report.Evidence.Regressions, comparison)
		}
	}
	report.Limitations = appendReviewLimitations(report.Limitations, scope.Limitations...)
	report.Limitations = appendReviewLimitations(report.Limitations, comparison.Limitations...)
	report.Limitations = appendReviewLimitations(report.Limitations, graph.Limitations...)
	report.Limitations = appendReviewLimitations(report.Limitations, architecture.Limitations...)
	if callGraph != nil {
		report.Limitations = appendReviewLimitations(report.Limitations, callGraph.Limitations...)
	} else {
		report.Limitations = appendReviewLimitations(report.Limitations, "affected-test evidence was not requested; use includeCallGraph to enable the Go compiler-backed call graph")
	}
	for _, diagnostic := range scope.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, ReviewDiagnostic{Source: "scope", Diagnostic: diagnostic})
	}
	for _, diagnostic := range comparison.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, ReviewDiagnostic{Source: diagnostic.Revision, Diagnostic: diagnostic.Diagnostic})
	}
	for _, diagnostic := range graph.Diagnostics {
		report.Diagnostics = append(report.Diagnostics, ReviewDiagnostic{Source: "graph", Diagnostic: diagnostic})
	}
	if callGraph != nil {
		for _, diagnostic := range callGraph.Diagnostics {
			report.Diagnostics = append(report.Diagnostics, ReviewDiagnostic{Source: "call-graph", Diagnostic: diagnostic})
		}
	}
	report.Findings = buildReviewFindings(comparison, scope.Callables, architecture, callGraph)
	report.Summary = ReviewSummary{
		ChangedFiles: scope.Summary.ChangedFiles, ChangedCallables: scope.Summary.ChangedCallables,
		NewRegressions: comparison.Summary.NewRegressions, QualityFindings: countReviewQualityFindings(report.Findings),
		AffectedTests: len(report.Evidence.AffectedTests), ArchitectureViolations: len(architecture.Violations),
		ArchitectureCycles: len(architecture.Cycles), Limitations: len(report.Limitations), Diagnostics: len(report.Diagnostics),
		Complete: comparison.Summary.Complete && architecture.Summary.Complete && !scope.Summary.Truncated && reviewCallGraphComplete(callGraph),
	}
	return report
}

func buildReviewFindings(comparison ChangeScopeComparisonReport, callables []MethodResult, architecture ArchitectureReport, callGraph *CallGraphReport) []ReviewFinding {
	findings := make([]ReviewFinding, 0)
	for _, item := range comparison.Callables {
		if !item.NewRegression {
			continue
		}
		method := reviewCallable(item.Current, item.Baseline)
		findings = append(findings, ReviewFinding{
			ID: reviewFindingID("regression", item.ID), Kind: "regression", Priority: reviewFindingRegressionRank,
			Severity: "error", Title: "new quality regression", Evidence: reviewMethodEvidence(item.ID, method, item.Reasons),
		})
	}
	for _, method := range callables {
		lowCoverage := method.CoveragePercent == nil || *method.CoveragePercent < ReviewLowCoveragePercent
		if !method.AboveThreshold && !lowCoverage {
			continue
		}
		kind, priority, title := "quality", reviewFindingQualityRank, "changed callable exceeds CRAP threshold"
		if !method.AboveThreshold {
			kind, priority, title = "coverage", reviewFindingCoverageRank, "changed callable has low or unknown coverage"
		}
		findings = append(findings, ReviewFinding{
			ID: reviewFindingID(kind, method.ID), Kind: kind, Priority: priority, Severity: "warning", Title: title,
			Evidence: reviewMethodEvidence(method.ID, method, nil),
		})
	}
	for _, test := range reviewAffectedTests(callGraph) {
		findings = append(findings, ReviewFinding{
			ID: reviewFindingID("test-impact", test.Test+"\x00"+test.File+"\x00"+test.Name), Kind: "test-impact", Priority: reviewFindingTestRank,
			Severity: "info", Title: "affected test candidate", Evidence: ReviewFindingEvidence{Test: test.Name, TestFile: test.File, Distance: test.Distance},
		})
	}
	for _, violation := range architecture.Violations {
		findings = append(findings, ReviewFinding{
			ID: reviewFindingID("architecture", violation.Kind+"\x00"+violation.From+"\x00"+violation.To), Kind: "architecture", Priority: reviewFindingArchitectureRank,
			Severity: "warning", Title: "architecture rule violation in selected source", Evidence: ReviewFindingEvidence{From: violation.From, To: violation.To, Reason: violation.Reason},
		})
	}
	for _, cycle := range architecture.Cycles {
		findings = append(findings, ReviewFinding{
			ID: reviewFindingID("architecture-cycle", strings.Join(cycle.Modules, "\x00")), Kind: "architecture", Priority: reviewFindingArchitectureRank,
			Severity: "warning", Title: "architecture import cycle in selected source", Evidence: ReviewFindingEvidence{Modules: append([]string(nil), cycle.Modules...)},
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Priority != findings[j].Priority {
			return findings[i].Priority < findings[j].Priority
		}
		if findings[i].Kind != findings[j].Kind {
			return findings[i].Kind < findings[j].Kind
		}
		return findings[i].ID < findings[j].ID
	})
	return findings
}

func reviewMethodEvidence(id string, method MethodResult, reasons []string) ReviewFindingEvidence {
	if method.ID == "" {
		method.ID = id
	}
	crap := method.CRAP
	return ReviewFindingEvidence{CallableID: method.ID, File: method.File, Name: method.Name, StartLine: method.StartLine, CRAP: &crap, CoveragePercent: method.CoveragePercent, Reasons: append([]string(nil), reasons...)}
}

func reviewCallable(current, baseline []ComparedCallable) MethodResult {
	if len(current) > 0 {
		return current[0].Method
	}
	if len(baseline) > 0 {
		return baseline[0].Method
	}
	return MethodResult{}
}

func reviewAffectedTests(callGraph *CallGraphReport) []CallGraphAffectedTest {
	if callGraph == nil {
		return nil
	}
	return callGraph.AffectedTests
}

func countReviewQualityFindings(findings []ReviewFinding) int {
	count := 0
	for _, finding := range findings {
		if finding.Kind == "quality" || finding.Kind == "coverage" {
			count++
		}
	}
	return count
}

func reviewCallGraphComplete(callGraph *CallGraphReport) bool {
	return callGraph == nil || (!callGraph.Summary.TruncatedCallSites && !callGraph.Summary.TruncatedUnresolved)
}

func reviewFindingID(kind, value string) string {
	return reportcontract.Fingerprint("review-finding-v1", kind, value)
}

func appendReviewLimitations(existing []string, values ...string) []string {
	seen := make(map[string]bool, len(existing)+len(values))
	for _, value := range existing {
		seen[value] = true
	}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			existing = append(existing, value)
		}
	}
	return existing
}

func reviewGrammars(groups ...[]GrammarIdentity) []GrammarIdentity {
	seen := make(map[string]GrammarIdentity)
	for _, grammars := range groups {
		for _, grammar := range grammars {
			seen[grammar.Language+"\x00"+grammar.Version] = grammar
		}
	}
	result := make([]GrammarIdentity, 0, len(seen))
	for _, grammar := range seen {
		result = append(result, grammar)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Language != result[j].Language {
			return result[i].Language < result[j].Language
		}
		return result[i].Version < result[j].Version
	})
	return result
}
