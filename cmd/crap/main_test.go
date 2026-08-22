package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/sarif"
)

type errorWriter struct {
	err error
}

func (writer errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

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
	for _, args := range [][]string{{"-h"}, {"--help"}, {"scope", "actual", "--help"}, {"compare", "--help"}, {"graph", "--help"}, {"mcp", "--help"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("run(%v) exit code = %d, stderr = %s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
			t.Errorf("run(%v) stdout = %q, stderr = %q", args, stdout.String(), stderr.String())
		}
	}
}

func TestRunCodeGraphJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\ntype Work struct{}\nfunc Run() {}\n"), 0o600); err != nil {
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
	if code := run([]string{"graph", "--format", "json", "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var report analysis.CodeGraphReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ReportType != "code-graph" || report.Summary.Files != 1 || report.Summary.Modules != 1 || report.Summary.Types != 1 || report.Summary.Callables != 1 || report.Summary.Edges != 3 {
		t.Fatalf("report = %#v", report)
	}
}

func TestWriteGraphTextRendersEveryNodeKindAndLimitations(t *testing.T) {
	report := analysis.CodeGraphReport{
		Summary: analysis.CodeGraphSummary{Nodes: 4, Edges: 3, Modules: 1, Files: 1, Types: 1, Callables: 1, References: 1, ResolvedReferences: 1},
		Nodes: []analysis.CodeGraphNode{
			{ID: "module", Kind: "module", Module: &analysis.CodeGraphModuleIdentity{System: "go-package", Name: "example.test/src"}},
			{ID: "file", Kind: "file", Path: filepath.Join("src", "work.go")},
			{Kind: "type", DeclarationKind: "struct", Name: "Work", Path: filepath.Join("src", "work.go"), Location: &analysis.CodeGraphLocation{StartLine: 3}},
			{Kind: "callable", DeclarationKind: "method", Name: "Run", Path: filepath.Join("src", "work.go"), Location: &analysis.CodeGraphLocation{StartLine: 5}, Metrics: &analysis.CodeGraphMetrics{CRAP: 4.25}},
		},
		References:  []analysis.CodeGraphReference{{Kind: "go-import", SourceFile: "file", Specifier: "example.test/dep", Location: analysis.CodeGraphLocation{StartLine: 2}, Resolution: "resolved", Target: "dependency"}},
		Limitations: []string{"lexical relationships only"},
	}
	var output bytes.Buffer
	if err := writeGraphText(&output, report); err != nil {
		t.Fatal(err)
	}
	want := "4 nodes and 3 edges: 1 modules, 1 files, 1 types, 1 callables; 1 references (1 resolved)\n" +
		"MODULE go-package example.test/src\n" +
		"FILE src/work.go\n" +
		"TYPE struct Work (src/work.go:3)\n" +
		"CALLABLE method Run (src/work.go:5) CRAP 4.25\n" +
		"REFERENCE go-import src/work.go:2 example.test/dep -> dependency\n" +
		"limitation: lexical relationships only\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestWriteGraphTextReturnsWriterErrors(t *testing.T) {
	want := errors.New("write failed")
	if err := writeGraphText(errorWriter{err: want}, analysis.CodeGraphReport{}); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestRunComparisonJSONAndRegressionExit(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample.go")
	if err := os.WriteFile(source, []byte("package sample\n\nfunc Work(v int) int { return v }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, root, "init")
	runGitCLI(t, root, "add", ".")
	runGitCLI(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	current := `package sample

func Work(v int) int {
	if v > 0 { v++ }
	if v > 1 { v++ }
	if v > 2 { v++ }
	if v > 3 { v++ }
	if v > 4 { v++ }
	return v
}
`
	if err := os.WriteFile(source, []byte(current), 0o600); err != nil {
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
	code := run([]string{"compare", "--base", "HEAD", "--format", "json", "--fail-on-regression", "."}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var report analysis.ChangeScopeComparisonReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ReportType != "change-scope-comparison" || report.Summary.NewRegressions != 1 || len(report.Callables) != 1 || report.Callables[0].Reasons[len(report.Callables[0].Reasons)-1] != "threshold-crossed" {
		t.Fatalf("report = %#v", report)
	}
}

func TestWriteComparisonTextShowsChangesAndOmitsUnchanged(t *testing.T) {
	method := func(id, name, file string, crap float64) analysis.ComparedCallable {
		return analysis.ComparedCallable{Method: analysis.MethodResult{ID: id, Name: name, File: file, StartLine: 3, CRAP: crap}}
	}
	base := method(strings.Repeat("a", 64), "sample.Work", "old.go", 10)
	current := method(strings.Repeat("b", 64), "sample.Work", "new.go", 40)
	report := analysis.ChangeScopeComparisonReport{
		Summary: analysis.ComparisonSummary{Matched: 2, Added: 1, Removed: 1, Ambiguous: 1, NewRegressions: 2, Complete: false},
		Callables: []analysis.CallableComparison{
			{Status: "matched", Change: "modified", Baseline: []analysis.ComparedCallable{base}, Current: []analysis.ComparedCallable{current}, NewRegression: true},
			{Status: "matched", Change: "unchanged", Baseline: []analysis.ComparedCallable{base}, Current: []analysis.ComparedCallable{base}},
			{Status: "added", Change: "added", Current: []analysis.ComparedCallable{current}, NewRegression: true},
			{Status: "removed", Change: "removed", Baseline: []analysis.ComparedCallable{base}},
			{Status: "ambiguous", Change: "ambiguous", Baseline: []analysis.ComparedCallable{base}, Current: []analysis.ComparedCallable{current}},
		},
	}
	var output bytes.Buffer
	if err := writeComparisonText(&output, report); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{"2 new regressions", "REGRESSION modified", "REGRESSION added", "REMOVED removed", "AMBIGUOUS ambiguous", "comparison incomplete"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("output %q does not contain %q", text, expected)
		}
	}
	if strings.Contains(text, "MATCHED unchanged") {
		t.Fatalf("unchanged comparison was rendered: %q", text)
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
