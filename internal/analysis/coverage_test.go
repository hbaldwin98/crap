package analysis

import (
	"os"
	"path/filepath"
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
	got := methodCoverage(coverage.forFile("src/Example.cs"), 10, 11)
	if got == nil || *got != 50 {
		t.Fatalf("coverage = %v, want 50", got)
	}
	if got := methodCoverage(coverage.forFile("src/Example.cs"), 20, 30); got != nil {
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
	got := methodCoverage(coverage.forFile("sample.go"), 10, 14)
	if got == nil || *got != 66.67 {
		t.Fatalf("coverage = %v, want 66.67", got)
	}
}

func TestCoveragePathMatchingRejectsAmbiguousSuffixes(t *testing.T) {
	coverage := coverageData{
		"first/sample.go":  {{StartLine: 1, EndLine: 1, Statements: 1, Covered: true}},
		"second/sample.go": {{StartLine: 1, EndLine: 1, Statements: 1, Covered: false}},
	}
	if got := coverage.forFile("sample.go"); got != nil {
		t.Fatalf("ambiguous coverage match = %#v, want nil", got)
	}
}
