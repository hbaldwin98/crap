package analysis

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestBuildReviewReportRanksEvidenceDeterministically(t *testing.T) {
	coverage := 25.0
	regressionMethod := MethodResult{ID: strings.Repeat("1", 64), Language: "go", File: "work.go", Name: "Run", StartLine: 4, CRAP: 42, CoveragePercent: &coverage}
	qualityMethod := MethodResult{ID: strings.Repeat("2", 64), Language: "go", File: "work.go", Name: "Check", StartLine: 8, CRAP: 35, AboveThreshold: true}
	comparison := ChangeScopeComparisonReport{
		Summary: ComparisonSummary{NewRegressions: 1, Complete: true},
		Callables: []CallableComparison{{
			ID: strings.Repeat("3", 64), Status: "matched", MatchStrategy: "id", Change: "modified", NewRegression: true,
			Current: []ComparedCallable{{Method: regressionMethod}}, Reasons: []string{"crap-increased"},
		}},
	}
	callGraph := &CallGraphReport{AffectedTests: []CallGraphAffectedTest{{
		Test: strings.Repeat("4", 64), Name: "TestRun", File: "work_test.go", Distance: 1, Seeds: []string{regressionMethod.ID},
	}}}
	architecture := ArchitectureReport{
		Summary:     ArchitectureSummary{Modules: 2, Edges: 1, Cycles: 1, Violations: 1, Complete: false},
		Cycles:      []ArchitectureCycle{{Modules: []string{"a", "b"}}},
		Violations:  []ArchitectureViolation{{Kind: "forbid", From: "a", To: "b", Reason: "layer boundary"}},
		Limitations: []string{"architecture is static"},
	}
	graph := CodeGraphReport{Summary: CodeGraphSummary{Callables: 2}, Coverage: CoverageMetadata{Format: "none"}}
	scope := ChangeScopeReport{Summary: ChangeScopeSummary{ChangedFiles: 1, ChangedCallables: 2}, Callables: []MethodResult{qualityMethod}}
	options := ReviewOptions{BaseRevision: "HEAD", Analysis: Options{CRAPThreshold: 30}}

	first := buildReviewReport(options, scope, comparison, graph, architecture, callGraph)
	second := buildReviewReport(options, scope, comparison, graph, architecture, callGraph)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("review report is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if got, want := len(first.Findings), 5; got != want {
		t.Fatalf("finding count = %d, want %d", got, want)
	}
	for index, want := range []string{"regression", "quality", "test-impact", "architecture", "architecture"} {
		if first.Findings[index].Kind != want {
			t.Fatalf("finding[%d].kind = %q, want %q", index, first.Findings[index].Kind, want)
		}
	}
	if first.Summary.NewRegressions != 1 || first.Summary.QualityFindings != 1 || first.Summary.AffectedTests != 1 || first.Summary.ArchitectureViolations != 1 || first.Summary.ArchitectureCycles != 1 {
		t.Fatalf("unexpected review summary: %#v", first.Summary)
	}
	if first.Summary.Complete {
		t.Fatal("incomplete architecture evidence was reported complete")
	}
}

func TestBuildReviewReportCreatesCoverageFindingForUnknownCoverage(t *testing.T) {
	method := MethodResult{ID: strings.Repeat("a", 64), Language: "go", File: "work.go", Name: "Run", StartLine: 2, CRAP: 2}
	report := buildReviewReport(
		ReviewOptions{BaseRevision: "HEAD"},
		ChangeScopeReport{Callables: []MethodResult{method}},
		ChangeScopeComparisonReport{Summary: ComparisonSummary{Complete: true}},
		CodeGraphReport{}, ArchitectureReport{}, nil,
	)
	if len(report.Findings) != 1 || report.Findings[0].Kind != "coverage" || report.Findings[0].Priority != reviewFindingCoverageRank {
		t.Fatalf("findings = %#v", report.Findings)
	}
	if len(report.Limitations) == 0 || !strings.Contains(report.Limitations[len(report.Limitations)-1], "affected-test evidence") {
		t.Fatalf("missing call graph limitation: %#v", report.Limitations)
	}
}

func TestValidateReviewOptions(t *testing.T) {
	if err := validateReviewOptions(ReviewOptions{}); err == nil {
		t.Fatal("missing base revision was accepted")
	}
	if err := validateReviewOptions(ReviewOptions{BaseRevision: "HEAD", Analysis: Options{DiffBase: "main"}}); err == nil {
		t.Fatal("analysis diff base was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	analyzer := &Analyzer{}
	if _, err := analyzer.AnalyzeReviewContext(ctx, ReviewOptions{BaseRevision: "HEAD"}); err == nil {
		t.Fatal("canceled review was accepted")
	}
}
