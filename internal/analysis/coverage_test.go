package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCoverageAndCalculateMethodCoverage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "coverage.xml")
	xml := `<coverage><packages><package><classes><class filename="src/Example.cs"><lines>` +
		`<line number="10" hits="1"/><line number="11" hits="0"/><line number="12" hits="2"/>` +
		`</lines></class></classes></package></packages></coverage>`
	if err := os.WriteFile(path, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}

	coverage, err := loadCoverage(path, root)
	if err != nil {
		t.Fatal(err)
	}
	got := methodCoverage(coverage.forFile("src/Example.cs").spans, []lineRange{{Start: 10, End: 11}})
	if got == nil || *got != 50 {
		t.Fatalf("coverage = %v, want 50", got)
	}
	if got := methodCoverage(coverage.forFile("src/Example.cs").spans, []lineRange{{Start: 20, End: 30}}); got != nil {
		t.Fatalf("uninstrumented method coverage = %v, want nil", *got)
	}
}

func TestParseGoCoverageUsesStatementCounts(t *testing.T) {
	profile := `mode: set
example.com/project/sample.go:10.1,12.2 2 1
example.com/project/sample.go:13.1,13.20 1 0
`
	coverage, err := parseGoCoverage(profile)
	if err != nil {
		t.Fatal(err)
	}
	got := methodCoverage(coverage.forFile("sample.go").spans, []lineRange{{Start: 10, End: 14}})
	if got == nil || *got != 66.67 {
		t.Fatalf("coverage = %v, want 66.67", got)
	}
}

func TestMethodCoverageExcludesBlocksCrossingCallableOwnership(t *testing.T) {
	spans := []coverageSpan{
		{StartLine: 10, EndLine: 12, Statements: 3, Covered: true},
		{StartLine: 13, EndLine: 13, Statements: 1, Covered: false},
	}
	got := methodCoverage(spans, []lineRange{{Start: 10, End: 10}, {Start: 13, End: 15}})
	if got == nil || *got != 0 {
		t.Fatalf("coverage = %v, want 0 from the fully owned block", got)
	}
}

func TestCoveragePathMatchingRejectsAmbiguousSuffixes(t *testing.T) {
	coverage := coverageData{loaded: true, files: map[string][]coverageSpan{
		"first/sample.go":  {{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}},
		"second/sample.go": {{StartLine: 1, EndLine: 1, Statements: 1, Covered: false}},
	}}
	if got := coverage.forFile("sample.go"); got.kind != "ambiguous" {
		t.Fatalf("ambiguous coverage match = %#v, want ambiguous", got)
	}
}

func TestCoveragePathMatchingIsPortableAndDeterministic(t *testing.T) {
	covered := []coverageSpan{{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}}
	coverage := coverageData{loaded: true, files: map[string][]coverageSpan{
		`C:/agent/work/src/Widget.ts`: covered,
	}}
	match := coverage.forFile(`src\Widget.ts`)
	if match.kind != "suffix" || len(match.spans) != 1 {
		t.Fatalf("portable match = %#v, want unique suffix", match)
	}

	coverage = coverageData{loaded: true, files: map[string][]coverageSpan{"SRC/widget.ts": covered}}
	match = coverage.forFile("src/Widget.ts")
	if match.kind != "case-folded" {
		t.Fatalf("case-folded match = %#v", match)
	}
}

func TestCoverageMatchingRejectsOneIdentityForMultipleSources(t *testing.T) {
	covered := []coverageSpan{{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}}
	coverage := coverageData{
		loaded:  true,
		files:   map[string][]coverageSpan{"work.ts": covered},
		aliases: map[string][]string{"a/work.ts": {"work.ts"}, "b/work.ts": {"work.ts"}},
	}
	matches := coverage.matchFiles([]string{"a/work.ts", "b/work.ts"})
	if len(matches) != 2 || matches[0].kind != "ambiguous" || matches[1].kind != "ambiguous" {
		t.Fatalf("cross-file matches = %#v", matches)
	}
}

func TestCoverageMatchingPrefersExactOwnerOverSuffix(t *testing.T) {
	covered := []coverageSpan{{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}}
	coverage := coverageData{loaded: true, files: map[string][]coverageSpan{"src/work.ts": covered}}
	matches := coverage.matchFiles([]string{"src/work.ts", "vendor/src/work.ts"})
	if matches[0].kind != "exact" || matches[1].kind != "unmatched" {
		t.Fatalf("precedence matches = %#v", matches)
	}
}

func TestCoverageMatchingRetriesWeakerOwner(t *testing.T) {
	covered := []coverageSpan{{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}}
	coverage := coverageData{loaded: true, files: map[string][]coverageSpan{
		"src/work.ts":        covered,
		"VENDOR/src/work.ts": covered,
	}}
	matches := coverage.matchFiles([]string{"src/work.ts", "vendor/src/work.ts"})
	if matches[0].kind != "exact" || matches[1].kind != "case-folded" {
		t.Fatalf("fallback matches = %#v", matches)
	}
}

func TestCoverageMatchingResolvesAmbiguityAfterExactClaim(t *testing.T) {
	covered := []coverageSpan{{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}}
	coverage := coverageData{loaded: true, files: map[string][]coverageSpan{
		"a/work.ts": covered,
		"b/work.ts": covered,
	}}
	matches := coverage.matchFiles([]string{"a/work.ts", "work.ts"})
	if matches[0].kind != "exact" || matches[1].kind != "suffix" || len(matches[1].identities) != 1 || matches[1].identities[0] != "b/work.ts" {
		t.Fatalf("resolved ambiguity = %#v", matches)
	}
}

func TestCoberturaRelativeSourceCreatesExactAlias(t *testing.T) {
	xml := `<coverage><sources><source>packages/app</source></sources><packages><package><classes><class filename="src/work.ts"><lines><line number="1" hits="1"/></lines></class></classes></package></packages></coverage>`
	coverage, err := parseCobertura([]byte(xml), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	match := coverage.matchFiles([]string{"packages/app/src/work.ts"})[0]
	if match.kind != "exact" {
		t.Fatalf("relative source match = %#v", match)
	}
}

func TestParseCoberturaUsesSourcesAndIgnoresMethodNames(t *testing.T) {
	root := normalizePortablePath(filepath.Join(t.TempDir(), "project"))
	xml := `<coverage><sources><source>` + root + `/wrong</source><source>` + root + `</source></sources><packages><package><classes>` +
		`<class filename="src/work.ts"><methods><method name="webpack_remapped_17"/></methods><lines>` +
		`<line number="2" hits="1"/></lines></class></classes></package></packages></coverage>`
	coverage, err := parseCobertura([]byte(xml), root)
	if err != nil {
		t.Fatal(err)
	}
	match := coverage.forFile("src/work.ts")
	if match.kind != "exact" || len(match.spans) != 1 || !match.spans[0].Covered {
		t.Fatalf("Cobertura match = %#v", match)
	}
}

func TestParseCoberturaMergesRelativeAndAbsoluteClassEntries(t *testing.T) {
	root := normalizePortablePath(t.TempDir())
	xml := `<coverage><packages><package><classes>` +
		`<class filename="src/work.ts"><lines><line number="1" hits="1"/></lines></class>` +
		`<class filename="` + root + `/src/work.ts"><lines><line number="2" hits="0"/></lines></class>` +
		`</classes></package></packages></coverage>`
	coverage, err := parseCobertura([]byte(xml), root)
	if err != nil {
		t.Fatal(err)
	}
	match := coverage.forFile("src/work.ts")
	if match.kind != "exact" || len(match.spans) != 2 {
		t.Fatalf("merged Cobertura match = %#v", match)
	}
}

func TestCoberturaCaseFoldedRootDoesNotBecomeExact(t *testing.T) {
	root := "/agent/repo"
	xml := `<coverage><packages><package><classes><class filename="/agent/Repo/work.ts"><lines><line number="1" hits="1"/></lines></class></classes></package></packages></coverage>`
	coverage, err := parseCobertura([]byte(xml), root)
	if err != nil {
		t.Fatal(err)
	}
	if match := coverage.forFile("work.ts"); match.kind == "exact" {
		t.Fatalf("case-folded root became exact: %#v", match)
	}
}

func TestCoberturaExternalAbsolutePathDoesNotLeak(t *testing.T) {
	root := "/agent/repo"
	external := "/producer/private/project/work.ts"
	xml := `<coverage><packages><package><classes><class filename="` + external + `"><lines><line number="1" hits="1"/></lines></class></classes></package></packages></coverage>`
	coverage, err := parseCobertura([]byte(xml), root)
	if err != nil {
		t.Fatal(err)
	}
	match := coverage.forFile("work.ts")
	if match.kind != "suffix" {
		t.Fatalf("external path match = %#v, want suffix", match)
	}
	for _, candidate := range match.candidates {
		if strings.Contains(candidate, "/producer/") {
			t.Fatalf("external path leaked in candidate %q", candidate)
		}
	}
}

func TestMethodCoverageExcludesNestedRanges(t *testing.T) {
	spans := []coverageSpan{
		{StartLine: 2, EndLine: 2, Statements: 1, Covered: true},
		{StartLine: 3, EndLine: 3, Statements: 1, Covered: true},
		{StartLine: 5, EndLine: 5, Statements: 1, Covered: false},
	}
	got := methodCoverage(spans, []lineRange{{Start: 1, End: 1}, {Start: 5, End: 6}})
	if got == nil || *got != 0 {
		t.Fatalf("owned coverage = %v, want 0", got)
	}
}
