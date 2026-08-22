package analysis

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/hbaldwin98/crap/internal/reportcontract"
)

func TestCompareChangeScopeReadsBaselineBlobsWithoutChangingWorktree(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "old.go")
	baselineSource := "package sample\n\nfunc Work(v int) int { return v }\nfunc Removed() {}\n"
	if err := os.WriteFile(oldPath, []byte(baselineSource), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	if err := os.Remove(oldPath); err != nil {
		t.Fatal(err)
	}
	currentSource := `package sample

func Work(v int) int {
	if v > 0 { v++ }
	if v > 1 { v++ }
	if v > 2 { v++ }
	if v > 3 { v++ }
	if v > 4 { v++ }
	return v
}
func Added() {}
`
	newPath := filepath.Join(root, "new.go")
	if err := os.WriteFile(newPath, []byte(currentSource), 0o600); err != nil {
		t.Fatal(err)
	}
	statusBefore := runGitOutput(t, root, "status", "--porcelain=v1")
	headBefore := runGitOutput(t, root, "rev-parse", "HEAD")
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.CompareChangeScope(ComparisonOptions{
		BaseRevision: "HEAD",
		Analysis:     Options{Root: root, CRAPThreshold: 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.MergeBase != strings.TrimSpace(headBefore) || report.Baseline.Commit != report.MergeBase {
		t.Fatalf("revision evidence = %#v", report)
	}
	if report.Summary.Matched != 1 || report.Summary.Added != 1 || report.Summary.Removed != 1 || report.Summary.NewRegressions != 1 || !report.Summary.Complete {
		t.Fatalf("summary = %#v", report.Summary)
	}
	var moved CallableComparison
	for _, comparison := range report.Callables {
		if comparison.Status == "matched" {
			moved = comparison
		}
	}
	if moved.MatchStrategy != "declaration" || moved.Change != "moved-modified" || !moved.NewRegression || moved.Delta == nil || moved.Delta.Complexity != 5 {
		t.Fatalf("moved comparison = %#v", moved)
	}
	if moved.Baseline[0].Method.File != "old.go" || moved.Current[0].Method.File != "new.go" || moved.Reasons[len(moved.Reasons)-1] != "threshold-crossed" {
		t.Fatalf("moved evidence = %#v", moved)
	}
	if got := runGitOutput(t, root, "status", "--porcelain=v1"); got != statusBefore {
		t.Fatalf("worktree status changed: before %q, after %q", statusBefore, got)
	}
	if got := runGitOutput(t, root, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD changed: before %q, after %q", headBefore, got)
	}
	data, err := os.ReadFile(newPath)
	if err != nil || string(data) != currentSource {
		t.Fatalf("current source changed: %v %q", err, data)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("baseline path restored into worktree: %v", err)
	}
	first, _ := json.Marshal(report)
	second, _ := json.Marshal(report)
	if string(first) != string(second) {
		t.Fatal("comparison JSON is not deterministic")
	}
}

func TestCompareCallablesReportsAmbiguousDeclarations(t *testing.T) {
	method := func(id, file string) MethodResult {
		return MethodResult{ID: id, Language: "go", File: file, Name: "sample.Work", Kind: "function", Signature: "func Work()", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 10, Complexity: 1, CRAP: 1}
	}
	baseline := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method(strings.Repeat("a", 64), "a.go"), method(strings.Repeat("b", 64), "b.go")}}
	current := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method(strings.Repeat("c", 64), "c.go"), method(strings.Repeat("d", 64), "d.go")}}
	comparisons, summary := compareCallables(t.Context(), baseline, current, 30, nil, nil)
	if len(comparisons) != 1 || comparisons[0].Status != "ambiguous" || len(comparisons[0].Baseline) != 2 || len(comparisons[0].Current) != 2 {
		t.Fatalf("comparisons = %#v", comparisons)
	}
	if summary.Ambiguous != 1 || summary.Complete {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestCompareCallablesDoesNotGateExistingDebtOrIncomparableCoverage(t *testing.T) {
	baselineCoverage, currentCoverage := 80.0, 10.0
	base := MethodResult{ID: strings.Repeat("a", 64), Language: "go", File: "work.go", Name: "sample.Work", Kind: "function", Signature: "func Work()", Complexity: 10, CoveragePercent: &baselineCoverage, CRAP: 40, AboveThreshold: true}
	now := base
	now.Complexity, now.CoveragePercent, now.CRAP = 11, &currentCoverage, 90
	comparisons, summary := compareCallables(t.Context(), Report{Coverage: CoverageMetadata{Format: "cobertura"}, Methods: []MethodResult{base}}, Report{Coverage: CoverageMetadata{Format: "cobertura"}, Methods: []MethodResult{now}}, 30, nil, nil)
	if summary.NewRegressions != 0 || comparisons[0].NewRegression {
		t.Fatalf("existing debt was gated: %#v", comparisons[0])
	}
	now.AboveThreshold = true
	base.AboveThreshold = false
	base.CoveragePercent = nil
	comparisons, summary = compareCallables(t.Context(), Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{base}}, Report{Coverage: CoverageMetadata{Format: "cobertura"}, Methods: []MethodResult{now}}, 30, nil, nil)
	if summary.NewRegressions != 0 || comparisons[0].Delta.ScoreComparable {
		t.Fatalf("incomparable score was gated: %#v", comparisons[0])
	}
}

func TestCompareCallablesDetectsBodyOnlyChanges(t *testing.T) {
	id := strings.Repeat("a", 64)
	method := MethodResult{ID: id, Language: "go", File: "work.go", Name: "sample.Work", Kind: "function", Signature: "func Work() int", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 29, Complexity: 1, CRAP: 2}
	baselineSource := []byte("func Work() int { return 1 }")
	currentSource := []byte("func Work() int { return 2 }")
	baseline := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method}}
	current := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method}}
	comparisons, _ := compareCallables(t.Context(), baseline, current, 30, map[string][]byte{"work.go": baselineSource}, map[string][]byte{"work.go": currentSource})
	if len(comparisons) != 1 || comparisons[0].Change != "modified" || comparisons[0].Baseline[0].ContentSHA256 == comparisons[0].Current[0].ContentSHA256 {
		t.Fatalf("comparison = %#v", comparisons)
	}
}

func TestCompareCallablesDoesNotExactMatchDuplicateDeclarations(t *testing.T) {
	method := func(id string) MethodResult {
		return MethodResult{ID: id, Language: "go", File: "work.go", Name: "sample.<anonymous>", Kind: "function_literal", Signature: "func()", Complexity: 1, CRAP: 2}
	}
	baseline := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method(strings.Repeat("a", 64)), method(strings.Repeat("b", 64))}}
	current := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method(strings.Repeat("a", 64))}}
	comparisons, summary := compareCallables(t.Context(), baseline, current, 30, nil, nil)
	if len(comparisons) != 1 || comparisons[0].Status != "ambiguous" || summary.Complete {
		t.Fatalf("comparisons = %#v, summary = %#v", comparisons, summary)
	}
}

func TestCompareCallablesExactIDSurvivesSignatureChange(t *testing.T) {
	id := strings.Repeat("a", 64)
	baseline := MethodResult{ID: id, Language: "go", File: "work.go", Name: "sample.Work", Kind: "function", Signature: "// old\nfunc Work()", Complexity: 5, CRAP: 40, AboveThreshold: true}
	current := baseline
	current.Signature = "// new\nfunc Work()"
	comparisons, summary := compareCallables(t.Context(), Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{baseline}}, Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{current}}, 30, nil, nil)
	if len(comparisons) != 1 || comparisons[0].Status != "matched" || comparisons[0].MatchStrategy != "id" || comparisons[0].NewRegression || summary.Matched != 1 || summary.Added != 0 || summary.Removed != 0 {
		t.Fatalf("comparisons = %#v, summary = %#v", comparisons, summary)
	}
}

func TestCompareCallablesRejectsMixedCoverageFormats(t *testing.T) {
	baseCoverage, currentCoverage := 100.0, 0.0
	base := MethodResult{ID: strings.Repeat("a", 64), Language: "go", File: "work.go", Name: "sample.Work", Kind: "function", Signature: "func Work()", Complexity: 5, CoveragePercent: &baseCoverage, CRAP: 5}
	now := base
	now.CoveragePercent, now.CRAP, now.AboveThreshold = &currentCoverage, 30, true
	comparisons, summary := compareCallables(t.Context(), Report{Coverage: CoverageMetadata{Format: "go-coverprofile"}, Methods: []MethodResult{base}}, Report{Coverage: CoverageMetadata{Format: "cobertura"}, Methods: []MethodResult{now}}, 20, nil, nil)
	if comparisons[0].Delta.ScoreComparable || comparisons[0].Delta.CoveragePercent != nil || summary.NewRegressions != 0 {
		t.Fatalf("mixed formats were comparable: %#v", comparisons[0])
	}
}

func TestCompareChangeScopeAcceptsDeletedExplicitFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deleted.go")
	if err := os.WriteFile(path, []byte("package sample\nfunc Removed() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.CompareChangeScope(ComparisonOptions{BaseRevision: "HEAD", Analysis: Options{Root: root, Paths: []string{"deleted.go"}, CRAPThreshold: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Removed != 1 || report.Current.Methods != 0 {
		t.Fatalf("report = %#v", report)
	}
}

func TestSelectedComparisonPathPrefersExplicitFile(t *testing.T) {
	explicit, selected := selectedComparisonPath("ignored/work.go", []string{"", "ignored/work.go"}, map[string]bool{"ignored/work.go": true})
	if !explicit || !selected {
		t.Fatalf("selectedComparisonPath = %v, %v", explicit, selected)
	}
}

func TestMatchedComparisonClassifiesDeltasAndRegressions(t *testing.T) {
	baselineCoverage, currentCoverage := 80.0, 60.0
	baseline := ComparedCallable{
		Method:        MethodResult{ID: strings.Repeat("a", 64), Language: "go", File: "old.go", Name: "sample.Work", Kind: "function", Signature: "func Work()", Complexity: 3, CoveragePercent: &baselineCoverage, CRAP: 3, AboveThreshold: false},
		CoverageState: "measured", ContentSHA256: strings.Repeat("1", 64),
	}
	current := ComparedCallable{
		Method:        MethodResult{ID: strings.Repeat("b", 64), Language: "go", File: "new.go", Name: "sample.Work", Kind: "function", Signature: "func Work()", Complexity: 5, CoveragePercent: &currentCoverage, CRAP: 31, AboveThreshold: true},
		CoverageState: "measured", ContentSHA256: strings.Repeat("2", 64),
	}
	comparison := matchedComparison(baseline, current, "declaration", 30, true)
	if comparison.Change != "moved-modified" || !comparison.NewRegression || comparison.MatchStrategy != "declaration" {
		t.Fatalf("comparison = %#v", comparison)
	}
	if comparison.Delta == nil || comparison.Delta.Complexity != 2 || comparison.Delta.CoveragePercent == nil || *comparison.Delta.CoveragePercent != -20 || comparison.Delta.CRAP == nil || *comparison.Delta.CRAP != 28 || !comparison.Delta.ScoreComparable {
		t.Fatalf("delta = %#v", comparison.Delta)
	}
	wantReasons := []string{"complexity-increased", "coverage-decreased", "crap-increased", "threshold-crossed"}
	if strings.Join(comparison.Reasons, ",") != strings.Join(wantReasons, ",") {
		t.Fatalf("reasons = %v", comparison.Reasons)
	}

	current.Method.File = baseline.Method.File
	current.ContentSHA256 = baseline.ContentSHA256
	current.Method.Complexity = baseline.Method.Complexity
	current.Method.CoveragePercent = baseline.Method.CoveragePercent
	current.Method.CRAP = baseline.Method.CRAP
	current.Method.AboveThreshold = false
	comparison = matchedComparison(baseline, current, "id", 30, true)
	if comparison.Change != "unchanged" || comparison.NewRegression || len(comparison.Reasons) != 0 {
		t.Fatalf("unchanged comparison = %#v", comparison)
	}

	current.Method.File = "new.go"
	comparison = matchedComparison(baseline, current, "declaration", 30, true)
	if comparison.Change != "moved" {
		t.Fatalf("move comparison = %#v", comparison)
	}

	current.Method.File = baseline.Method.File
	current.ContentSHA256 = strings.Repeat("3", 64)
	comparison = matchedComparison(baseline, current, "id", 30, true)
	if comparison.Change != "modified" {
		t.Fatalf("modified comparison = %#v", comparison)
	}
}

func TestMatchedComparisonRequiresCompatibleCoverageEvidence(t *testing.T) {
	coverage := 50.0
	method := MethodResult{ID: strings.Repeat("a", 64), Language: "go", File: "work.go", Name: "sample.Work", Kind: "function", Signature: "func Work()", Complexity: 3, CoveragePercent: &coverage, CRAP: 3}
	tests := []struct {
		name       string
		baseline   string
		current    string
		sameFormat bool
		comparable bool
	}{
		{name: "measured", baseline: "measured", current: "measured", sameFormat: true, comparable: true},
		{name: "absent", baseline: "absent", current: "absent", sameFormat: true, comparable: true},
		{name: "mixed state", baseline: "absent", current: "measured", sameFormat: true, comparable: false},
		{name: "mixed format", baseline: "measured", current: "measured", sameFormat: false, comparable: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			comparison := matchedComparison(ComparedCallable{Method: method, CoverageState: test.baseline}, ComparedCallable{Method: method, CoverageState: test.current}, "id", 30, test.sameFormat)
			if comparison.Delta.ScoreComparable != test.comparable {
				t.Fatalf("comparable = %v", comparison.Delta.ScoreComparable)
			}
			if !test.sameFormat && comparison.Delta.CoveragePercent != nil {
				t.Fatalf("coverage delta = %v", comparison.Delta.CoveragePercent)
			}
			if test.comparable != (comparison.Delta.CRAP != nil) {
				t.Fatalf("CRAP delta = %v", comparison.Delta.CRAP)
			}
		})
	}
}

func TestCompareCallablesDoesNotGateAddedCallableAtThreshold(t *testing.T) {
	method := MethodResult{ID: strings.Repeat("a", 64), Language: "go", File: "work.go", Name: "sample.Work", Kind: "function", Signature: "func Work()", Complexity: 5, CRAP: 30, AboveThreshold: false}
	comparisons, summary := compareCallables(t.Context(), Report{Coverage: CoverageMetadata{Format: "none"}}, Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method}}, 30, nil, nil)
	if len(comparisons) != 1 || comparisons[0].Status != "added" || comparisons[0].NewRegression || len(comparisons[0].Reasons) != 0 || summary.Added != 1 || summary.NewRegressions != 0 {
		t.Fatalf("comparisons = %#v, summary = %#v", comparisons, summary)
	}
	method.CRAP, method.AboveThreshold = 30.01, true
	comparisons, summary = compareCallables(t.Context(), Report{Coverage: CoverageMetadata{Format: "none"}}, Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method}}, 30, nil, nil)
	if !comparisons[0].NewRegression || strings.Join(comparisons[0].Reasons, ",") != "added-above-threshold" || summary.NewRegressions != 1 {
		t.Fatalf("comparisons = %#v, summary = %#v", comparisons, summary)
	}
}

func TestComparisonCoordinateAndDeltaHelpers(t *testing.T) {
	value := 10.0
	if nullableDelta(nil, &value) != nil || nullableDelta(&value, nil) != nil || coverageDeltaValue(nil) != 0 {
		t.Fatal("nil deltas were not preserved")
	}
	current := 7.25
	delta := nullableDelta(&value, &current)
	if delta == nil || *delta != -2.75 || coverageDeltaValue(delta) != -2.75 {
		t.Fatalf("delta = %v", delta)
	}
	data := []byte("one\ntwo\nthree")
	tests := []struct {
		line, column int
		offset       int
		ok           bool
	}{
		{line: 1, column: 1, offset: 0, ok: true},
		{line: 2, column: 2, offset: 5, ok: true},
		{line: 3, column: 6, offset: len(data), ok: true},
		{line: 0, column: 1, ok: false},
		{line: 1, column: 0, ok: false},
		{line: 4, column: 1, ok: false},
		{line: 1, column: 5, offset: 4, ok: false},
	}
	for _, test := range tests {
		offset, ok := sourceCoordinateOffset(data, test.line, test.column)
		if offset != test.offset || ok != test.ok {
			t.Fatalf("sourceCoordinateOffset(%d, %d) = %d, %v", test.line, test.column, offset, ok)
		}
	}
	method := MethodResult{StartLine: 2, StartColumn: 1, EndLine: 2, EndColumn: 4}
	if got := callableContentSHA256(method, data); got != reportcontract.SHA256([]byte("two")) {
		t.Fatalf("callable hash = %s", got)
	}
	method.StartLine = 9
	if got := callableContentSHA256(method, data); got != reportcontract.SHA256(data) {
		t.Fatalf("fallback hash = %s", got)
	}
}

func TestCompareCallablesHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	method := MethodResult{ID: strings.Repeat("a", 64), Language: "go", Name: "sample.Work", Kind: "function", Signature: "func Work()"}
	baseline := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method, method}}
	current := Report{Coverage: CoverageMetadata{Format: "none"}, Methods: []MethodResult{method, method}}
	comparisons, summary := compareCallables(ctx, baseline, current, 30, nil, nil)
	if comparisons != nil || summary != (ComparisonSummary{}) {
		t.Fatalf("comparisons = %#v, summary = %#v", comparisons, summary)
	}
}

func TestParseGitTreeValidatesAndSortsEntries(t *testing.T) {
	data := []byte("100644 blob bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\tb.go\x00100755 blob aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\ta.go\x00")
	entries, err := parseGitTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].path != "a.go" || entries[0].mode != "100755" || entries[1].path != "b.go" {
		t.Fatalf("entries = %#v", entries)
	}
	for _, malformed := range [][]byte{
		[]byte("no-tab"),
		[]byte("100644 blob\twork.go"),
		[]byte("100644 blob abc\t"),
		[]byte("100644 blob aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\tdir\\work.go"),
	} {
		if _, err := parseGitTree(malformed); err == nil {
			t.Fatalf("parseGitTree(%q) succeeded", malformed)
		}
	}
}

func TestBaselineSourceSelectorClassifiesDiscoveryPolicies(t *testing.T) {
	excludes, err := comparisonExcludeMatcher([]string{"excluded/**"})
	if err != nil {
		t.Fatal(err)
	}
	selector := baselineSourceSelector{
		prefix:        "src",
		selectors:     []string{""},
		explicitFiles: map[string]bool{"explicit_test.go": true},
		excludes:      excludes,
		gitIgnore:     gitignore.NewMatcher(parseIgnoreData([]byte("ignored.go\n"), []string{"src"})),
		crapIgnore:    gitignore.NewMatcher(parseIgnoreData([]byte("crap.go\n"), nil)),
	}
	tests := []struct {
		name     string
		entry    gitTreeEntry
		relative string
		reason   string
		selected bool
	}{
		{name: "regular", entry: gitTreeEntry{path: "src/work.go", typ: "blob", mode: "100644"}, relative: "work.go", selected: true},
		{name: "exclusion", entry: gitTreeEntry{path: "src/excluded/work.go", typ: "blob", mode: "100644"}, relative: "excluded/work.go", reason: "explicit", selected: true},
		{name: "gitignore", entry: gitTreeEntry{path: "src/ignored.go", typ: "blob", mode: "100644"}, relative: "ignored.go", reason: "gitignore", selected: true},
		{name: "crapignore", entry: gitTreeEntry{path: "src/crap.go", typ: "blob", mode: "100644"}, relative: "crap.go", reason: "crapignore", selected: true},
		{name: "test", entry: gitTreeEntry{path: "src/work_test.go", typ: "blob", mode: "100644"}, relative: "work_test.go", reason: "test", selected: true},
		{name: "generated", entry: gitTreeEntry{path: "src/work.generated.ts", typ: "blob", mode: "100644"}, relative: "work.generated.ts", reason: "generated", selected: true},
		{name: "explicit override", entry: gitTreeEntry{path: "src/explicit_test.go", typ: "blob", mode: "100644"}, relative: "explicit_test.go", selected: true},
		{name: "nonregular", entry: gitTreeEntry{path: "src/link.go", typ: "blob", mode: "120000"}, relative: "link.go", reason: "git-mode", selected: true},
		{name: "unsupported", entry: gitTreeEntry{path: "src/readme.md", typ: "blob", mode: "100644"}, selected: false},
		{name: "outside root", entry: gitTreeEntry{path: "other/work.go", typ: "blob", mode: "100644"}, selected: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relative, reason, selected := selector.classify(test.entry)
			if relative != test.relative || reason != test.reason || selected != test.selected {
				t.Fatalf("classify = %q, %q, %v", relative, reason, selected)
			}
		})
	}
}

func TestComparisonSelectionHelpers(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "work.go")
	selectors, explicit, err := comparisonSelectors(root, []string{".", inside})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selectors, ",") != ",work.go" || !explicit["work.go"] {
		t.Fatalf("selectors = %v, explicit = %v", selectors, explicit)
	}
	defaults, _, err := comparisonSelectors(root, nil)
	if err != nil || len(defaults) != 1 || defaults[0] != "" {
		t.Fatalf("default selectors = %v, %v", defaults, err)
	}
	if _, _, err := comparisonSelectors(root, []string{"../outside.go"}); err == nil {
		t.Fatal("outside comparison path was accepted")
	}
	for _, pattern := range []string{"", "!work.go"} {
		if _, err := comparisonExcludeMatcher([]string{pattern}); err == nil {
			t.Fatalf("exclude %q was accepted", pattern)
		}
	}
	if matcher, err := comparisonExcludeMatcher(nil); err != nil || matcher != nil {
		t.Fatalf("empty matcher = %v, %v", matcher, err)
	}
	if relative, ok := pathBelowPrefix("src/work.go", "src"); !ok || relative != "work.go" {
		t.Fatalf("pathBelowPrefix = %q, %v", relative, ok)
	}
	if relative, ok := pathBelowPrefix("src", "src"); !ok || relative != "" {
		t.Fatalf("root pathBelowPrefix = %q, %v", relative, ok)
	}
	if _, ok := pathBelowPrefix("other/work.go", "src"); ok {
		t.Fatal("outside prefix was accepted")
	}
	if explicit, selected := selectedComparisonPath("work.go", []string{"other"}, nil); explicit || selected {
		t.Fatalf("unselected path = %v, %v", explicit, selected)
	}
}

func TestBaselineDirectoryWithSourceSuffixRemainsRecursive(t *testing.T) {
	explicit := map[string]bool{"pkg.go": true}
	entries := []gitTreeEntry{{path: "pkg.go/work.go", typ: "blob", mode: "100644"}}
	demoteBaselineDirectorySelectors("", entries, explicit)
	if explicit["pkg.go"] {
		t.Fatalf("directory remained explicit: %v", explicit)
	}
	selector := baselineSourceSelector{selectors: []string{"pkg.go"}, explicitFiles: explicit}
	relative, reason, selected := selector.classify(entries[0])
	if relative != "pkg.go/work.go" || reason != "" || !selected {
		t.Fatalf("classify = %q, %q, %v", relative, reason, selected)
	}
}

func TestComparisonConfigFingerprintUsesSemanticPathsAndExcludes(t *testing.T) {
	root := t.TempDir()
	baseline := Report{Grammars: []GrammarIdentity{}, Diagnostics: []Diagnostic{}, Fingerprints: reportcontract.Fingerprints{Sources: []reportcontract.FileFingerprint{}}}
	current := baseline
	revision := gitChangeRequest{}
	left := newComparisonReport(root, ComparisonOptions{Analysis: Options{Paths: []string{"."}, Exclude: []string{"vendor/**", "vendor/**"}}}, revision, baseline, current)
	right := newComparisonReport(root, ComparisonOptions{Analysis: Options{Paths: []string{root}, Exclude: []string{"vendor/**"}}}, revision, baseline, current)
	if left.ConfigSHA256 != right.ConfigSHA256 {
		t.Fatalf("config fingerprints differ: %s != %s", left.ConfigSHA256, right.ConfigSHA256)
	}
}

func TestDiscoveryRecorderDeduplicatesAndBoundsExamples(t *testing.T) {
	result := discoveryResult{exclusions: make(map[string]int), examples: make(map[string][]string)}
	recorder := discoveryRecorder{result: &result, seen: make(map[string]map[string]bool)}
	recorder.record("test", "b.go")
	recorder.record("test", "b.go")
	for _, path := range []string{"f.go", "e.go", "d.go", "c.go", "a.go"} {
		recorder.record("test", path)
	}
	if result.exclusions["test"] != 6 || len(result.examples["test"]) != discoveryExampleLimit || strings.Join(result.examples["test"], ",") != "a.go,b.go,c.go,d.go,e.go" {
		t.Fatalf("result = %#v", result)
	}
}

func runGitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return string(output)
}
