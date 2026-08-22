package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/sarif"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != buildinfo.CurrentVersion()+"\n" {
		t.Fatalf("version output = %q", stdout.String())
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"scope", "actual", "--help"}, {"mcp", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("run(%v) exit code = %d, stderr = %s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
			t.Errorf("run(%v) stdout = %q, stderr = %q", args, stdout.String(), stderr.String())
		}
	}
}

func TestRunActualChangeScopeJSON(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample.go")
	if err := os.WriteFile(source, []byte("package sample\n\nfunc Changed() int { return 1 }\nfunc Stable() int { return 2 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, root, "init")
	runGitCLI(t, root, "add", ".")
	runGitCLI(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	if err := os.WriteFile(source, []byte("package sample\n\nfunc Changed() int { return 3 }\nfunc Stable() int { return 2 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scope", "actual", "--format", "json", "--diff-base", "HEAD", "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var report analysis.ChangeScopeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ReportType != "change-scope" || len(report.Files) != 1 || len(report.Callables) != 1 || report.Callables[0].Name != "sample.Changed" {
		t.Fatalf("report = %#v", report)
	}
}

func TestWriteScopeTextIncludesChangedRanges(t *testing.T) {
	report := analysis.ChangeScopeReport{
		Summary: analysis.ChangeScopeSummary{ChangedFiles: 1},
		Files: []analysis.ChangeScopeFile{{
			Path: "src/work.go",
			Ranges: []analysis.ChangeScopeRange{
				{StartLine: 3, EndLine: 3},
				{StartLine: 7, EndLine: 9},
			},
		}},
		Callables:   []analysis.MethodResult{},
		Limitations: []string{},
	}
	var output bytes.Buffer
	if err := writeScopeText(&output, report); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"src/work.go:3 changed", "src/work.go:7-9 changed"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestRunVersionTakesPrecedenceOverSemanticValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version", "--format", "yaml"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsTrailingMCPArguments(t *testing.T) {
	for _, args := range [][]string{{"mcp", "unexpected"}, {"mcp", "--help", "unexpected"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Fatalf("run(%v) exit code = %d, stderr = %s", args, code, stderr.String())
		}
	}
}

func TestRunJSONAndThreshold(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\nfunc Work(ok bool) { if ok { return } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--format", "json", "--threshold", "1", "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var report analysis.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Methods) != 1 || report.Methods[0].CRAP != 6 {
		t.Fatalf("unexpected report: %#v", report)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--threshold", "1", "--fail-on-threshold", "."}, &stdout, &stderr); code != 2 {
		t.Fatalf("threshold exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sample.Work") {
		t.Fatalf("text report does not contain callable: %s", stdout.String())
	}
}

func TestRunWritesOutputWithoutReportOnStdout(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })

	var stdout, stderr bytes.Buffer
	destination := filepath.Join(root, "report.json")
	if code := run([]string{"--format", "json", "--output", destination, "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var report analysis.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Methods) != 1 {
		t.Fatalf("methods = %d", len(report.Methods))
	}
}

func TestAnalysisSARIFIsDeterministicAndContainsViolations(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "work.go"), []byte("é😀work()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage := 75.0
	report := analysis.Report{Threshold: 10, Methods: []analysis.MethodResult{
		{ID: "ignored", File: "src/ok.go", Name: "ok", StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 10},
		{ID: "method-id", File: `src\work.go`, Name: "work", StartLine: 1, StartColumn: 3, EndLine: 1, EndColumn: 7, Complexity: 5, CoveragePercent: &coverage, CRAP: 12.5, AboveThreshold: true},
	}}
	var first, second bytes.Buffer
	if err := writeReport(&first, report, "sarif", root); err != nil {
		t.Fatal(err)
	}
	if err := writeReport(&second, report, "sarif", root); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("SARIF output is not deterministic")
	}
	var document sarif.Log
	if err := json.Unmarshal(first.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "2.1.0" || len(document.Runs) != 1 || len(document.Runs[0].Results) != 1 {
		t.Fatalf("unexpected SARIF shape: %#v", document)
	}
	result := document.Runs[0].Results[0]
	region := result.Locations[0].PhysicalLocation.Region
	if result.RuleID != "CRAP001" || result.Locations[0].PhysicalLocation.ArtifactLocation.URI != "src/work.go" || region.StartLine != 1 || region.StartColumn != 2 || region.EndLine != 1 || region.EndColumn != 4 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.PartialFingerprints["crap.methodId/v1"] != "method-id" || result.PartialFingerprints["primaryLocationLineHash"] != "method-id" || document.Runs[0].Tool.Driver.Version != buildinfo.CurrentVersion() {
		t.Fatalf("unexpected identity: %#v", result.PartialFingerprints)
	}
	if document.Runs[0].ColumnKind != "utf16CodeUnits" || document.Runs[0].Tool.Driver.Rules[0].FullDescription.Text == "" || document.Runs[0].Tool.Driver.Rules[0].Help.Text == "" {
		t.Fatalf("incomplete GitHub profile: %#v", document.Runs[0])
	}
	typed, err := analysisSARIF(report, root)
	if err != nil {
		t.Fatal(err)
	}
	properties, ok := typed.Runs[0].Results[0].Properties.(analysisSARIFProperties)
	if !ok || properties.Score != 12.5 || properties.Complexity != 5 || properties.Coverage == nil || *properties.Coverage != 75 || properties.Threshold != 10 {
		t.Fatalf("properties = %#v", properties)
	}
}

func TestAnalysisSARIFHasEmptyResultsWithoutViolations(t *testing.T) {
	document, err := analysisSARIF(analysis.Report{Methods: []analysis.MethodResult{{ID: "ok"}}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Runs) != 1 || document.Runs[0].Results == nil || len(document.Runs[0].Results) != 0 {
		t.Fatalf("results = %#v", document.Runs)
	}
}

func TestAnalysisSARIFRejectsMoreThanGitHubResultLimit(t *testing.T) {
	methods := make([]analysis.MethodResult, sarif.MaxResults+1)
	for index := range methods {
		methods[index].AboveThreshold = true
	}
	if _, err := analysisSARIF(analysis.Report{Methods: methods}, t.TempDir()); err == nil {
		t.Fatal("SARIF result overflow was accepted")
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	tests := [][]string{
		{"--format", "yaml"},
		{"--threshold", "-1"},
		{"--threshold", "NaN"},
		{"--not-a-flag"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Errorf("run(%v) exit code = %d, want 1", args, code)
		}
		if stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("run(%v) stdout = %q, stderr = %q", args, stdout.String(), stderr.String())
		}
	}
}

func TestParseOptionsAcceptsStrictCoverage(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseOptions([]string{"--strict-coverage", "--include-generated", "--exclude", "dist/**", "--exclude", "vendor/**", "src"}, &stderr)
	if !ok || !options.strictCoverage || !options.includeGenerated || len(options.excludes) != 2 || len(options.paths) != 1 || options.paths[0] != "src" {
		t.Fatalf("options = %#v, ok = %v, stderr = %s", options, ok, stderr.String())
	}
}

func TestParseOptionsStopsAtFirstPathAndHonorsDoubleDash(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseOptions([]string{"src", "--threshold", "1"}, &stderr)
	if !ok || options.threshold != 30 || len(options.paths) != 3 || options.paths[1] != "--threshold" {
		t.Fatalf("options = %#v, ok = %v", options, ok)
	}
	options, ok = parseOptions([]string{"--threshold", "1", "--", "-source"}, &stderr)
	if !ok || options.threshold != 1 || len(options.paths) != 1 || options.paths[0] != "-source" {
		t.Fatalf("double-dash options = %#v, ok = %v", options, ok)
	}
}

func TestParseMCPOptionsRestrictsArguments(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseMCPOptions([]string{"--root", "project", "--allow-root", "one", "--allow-root", "two"}, &stderr, "crap")
	if !ok || options.root != "project" || len(options.allowRoots) != 2 {
		t.Fatalf("options = %#v, ok = %v, stderr = %s", options, ok, stderr.String())
	}
	if _, ok := parseMCPOptions([]string{"unexpected"}, &stderr, "crap"); ok {
		t.Fatal("positional MCP argument was accepted")
	}
}

func runGitCLI(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
