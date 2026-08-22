package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/hbaldwin98/crap/internal/sarif"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != buildinfo.CurrentVersion()+"\n" {
		t.Fatalf("version = %q", stdout.String())
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"mcp", "-h"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Errorf("run(%v) code = %d, stderr = %s", args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
			t.Errorf("run(%v) stdout = %q, stderr = %q", args, stdout.String(), stderr.String())
		}
	}
}

func TestRunVersionTakesPrecedenceOverSemanticValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version", "--format", "yaml"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunRejectsTrailingMCPArguments(t *testing.T) {
	for _, args := range [][]string{{"mcp", "unexpected"}, {"mcp", "--help", "unexpected"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Fatalf("run(%v) code = %d, stderr = %s", args, code, stderr.String())
		}
	}
}

func TestRunDryRunReturnsPlanWithoutExecutingEngine(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--language", "go", "--dry-run", "--format", "json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	var plan mutation.Plan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Engine != "gremlins" || plan.Arguments[0] != "unleash" || plan.ReportPath != "$REPORT_PATH" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRunDryRunWritesOutputWithoutReportOnStdout(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "plan.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--language", "go", "--dry-run", "--format", "json", "--output", destination}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var plan mutation.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Engine != "gremlins" {
		t.Fatalf("engine = %q", plan.Engine)
	}
}

func TestWriteDoctorText(t *testing.T) {
	var output bytes.Buffer
	report := mutation.DoctorReport{Ready: true, Checks: []mutation.DoctorCheck{{Name: "engine-version", Status: "passed", Message: "v1"}}}
	if err := writeDoctor(&output, report, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "engine-version") || !strings.Contains(output.String(), "ready: true") {
		t.Fatalf("output = %s", output.String())
	}
}

func TestWritePlanTextPreservesArgumentBoundaries(t *testing.T) {
	var output bytes.Buffer
	plan := mutation.Plan{Executable: "tool", Arguments: []string{"--path", "source files/work.ts"}}
	if err := writePlan(&output, plan, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"source files/work.ts"`) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestParseOptionsRejectsInvalidValues(t *testing.T) {
	for _, args := range [][]string{{}, {"--language", "rust"}, {"--language", "go", "--minimum-score", "101"}, {"--language", "go", "--minimum-score", "NaN"}, {"--language", "go", "--timeout", "1ms"}} {
		var stdout, stderr bytes.Buffer
		if _, ok := parseOptionsWithHelp(args, &stdout, &stderr); ok {
			t.Errorf("parseOptions(%v) succeeded", args)
		}
		if stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("parseOptions(%v) stdout = %q, stderr = %q", args, stdout.String(), stderr.String())
		}
	}
}

func TestWriteTextShowsActionableMutants(t *testing.T) {
	score := 50.0
	report := mutation.Report{Engine: "stryker-js", Score: &score, MinimumScore: 80, Summary: mutation.Summary{Total: 2, Survived: 1}, Mutants: []mutation.MutantResult{
		{File: "src/work.ts", Line: 2, Column: 3, Mutator: "BooleanLiteral", Status: "survived"},
		{File: "src/work.ts", Line: 4, Column: 1, Mutator: "StringLiteral", Status: "killed"},
	}}
	var output bytes.Buffer
	writeText(&output, report)
	if !strings.Contains(output.String(), "BooleanLiteral") || strings.Contains(output.String(), "StringLiteral") {
		t.Fatalf("output = %s", output.String())
	}
}

func TestMutationSARIFIsDeterministicAndHandlesPointAndRangeLocations(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "range.ts"), []byte("é😀value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	endLine, endColumn := 1, 4
	report := mutation.Report{Engine: "stryker-js", MinimumScore: 80, Mutants: []mutation.MutantResult{
		{ID: "killed", File: "src/ignored.ts", StartLine: 1, StartColumn: 1, Status: "killed", Mutator: "ignored"},
		{ID: "range-id", File: `src\range.ts`, StartLine: 1, StartColumn: 2, EndLine: &endLine, EndColumn: &endColumn, Status: "survived", Mutator: "BooleanLiteral"},
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
	document, err := mutationSARIF(report, root)
	if err != nil {
		t.Fatal(err)
	}
	if document.Version != "2.1.0" || len(document.Runs) != 1 || len(document.Runs[0].Tool.Driver.Rules) != 2 || len(document.Runs[0].Results) != 1 {
		t.Fatalf("unexpected SARIF shape: %#v", document)
	}
	ranged := document.Runs[0].Results[0]
	region := ranged.Locations[0].PhysicalLocation.Region
	if ranged.RuleID != "MUT001" || ranged.Locations[0].PhysicalLocation.ArtifactLocation.URI != "src/range.ts" || region.StartColumn != 2 || region.EndLine != 1 || region.EndColumn != 4 {
		t.Fatalf("range result = %#v", ranged)
	}
	if ranged.PartialFingerprints["crap-mutate.mutantId/v1"] != "range-id" || ranged.PartialFingerprints["primaryLocationLineHash"] != "range-id" {
		t.Fatalf("fingerprints = %#v", ranged.PartialFingerprints)
	}
	if document.Runs[0].Tool.Driver.Version != buildinfo.CurrentVersion() || document.Runs[0].ColumnKind != "utf16CodeUnits" {
		t.Fatalf("run profile = %#v", document.Runs[0])
	}
	for _, rule := range document.Runs[0].Tool.Driver.Rules {
		if rule.FullDescription.Text == "" || rule.Help.Text == "" {
			t.Fatalf("incomplete rule = %#v", rule)
		}
	}
	properties, ok := ranged.Properties.(mutationSARIFProperties)
	if !ok || properties.Engine != "stryker-js" || properties.Status != "survived" || properties.MinimumScore != 80 {
		t.Fatalf("properties = %#v", properties)
	}
}

func TestMutationSARIFConvertsGremlinsBytePointAndSynthesizesEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "point.go"), []byte("aé😀x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := mutation.Report{Engine: "gremlins", Mutants: []mutation.MutantResult{{
		ID: "point-id", File: "point.go", StartLine: 1, StartColumn: 4, Status: "noCoverage", Mutator: "CONDITIONALS_BOUNDARY",
	}}}
	document, err := mutationSARIF(report, root)
	if err != nil {
		t.Fatal(err)
	}
	result := document.Runs[0].Results[0]
	region := result.Locations[0].PhysicalLocation.Region
	if region.StartLine != 1 || region.StartColumn != 3 || region.EndLine != 1 || region.EndColumn != 5 {
		t.Fatalf("point region = %#v", region)
	}
}

func TestMutationSARIFHasEmptyResultsWithoutActionableMutants(t *testing.T) {
	document, err := mutationSARIF(mutation.Report{Mutants: []mutation.MutantResult{{Status: "killed"}}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Runs) != 1 || document.Runs[0].Results == nil || len(document.Runs[0].Results) != 0 {
		t.Fatalf("results = %#v", document.Runs)
	}
}

func TestMutationSARIFRejectsInvalidSourceAndResultOverflow(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work.ts"), []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stryker := mutation.Report{Engine: "stryker-js", Mutants: []mutation.MutantResult{{ID: "no-end", File: "work.ts", StartLine: 1, StartColumn: 1, Status: "survived"}}}
	if _, err := mutationSARIF(stryker, root); err == nil {
		t.Fatal("Stryker location without an end was accepted")
	}
	report := mutation.Report{Engine: "gremlins", Mutants: []mutation.MutantResult{{ID: "missing", File: "missing.go", StartLine: 1, StartColumn: 1, Status: "survived"}}}
	if _, err := mutationSARIF(report, root); err == nil {
		t.Fatal("missing source was accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	report.Mutants[0].File = "../outside.go"
	if _, err := mutationSARIF(report, root); err == nil {
		t.Fatal("outside source was accepted")
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(outside, link); err == nil {
		report.Mutants[0].File = "link.go"
		if _, err := mutationSARIF(report, root); err == nil {
			t.Fatal("symlink source was accepted")
		}
	}
	mutants := make([]mutation.MutantResult, sarif.MaxResults+1)
	for index := range mutants {
		mutants[index].Status = "survived"
	}
	if _, err := mutationSARIF(mutation.Report{Mutants: mutants}, root); err == nil {
		t.Fatal("SARIF result overflow was accepted")
	}
}

func TestRunRejectsSARIFForPlanAndDoctor(t *testing.T) {
	for _, args := range [][]string{{"--language", "go", "--dry-run", "--format", "sarif"}, {"doctor", "--language", "go", "--format", "sarif"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Errorf("run(%v) code = %d, stderr = %s", args, code, stderr.String())
		}
	}
}

func TestMutationExitCode(t *testing.T) {
	for _, test := range []struct {
		fail, passed bool
		want         int
	}{
		{fail: false, passed: false, want: 0},
		{fail: true, passed: true, want: 0},
		{fail: true, passed: false, want: 2},
	} {
		if got := mutationExitCode(test.fail, test.passed); got != test.want {
			t.Errorf("mutationExitCode(%t, %t) = %d, want %d", test.fail, test.passed, got, test.want)
		}
	}
}

func TestParseMCPOptionsRestrictsArguments(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseMCPOptions([]string{"--root", "project", "--allow-root", "other"}, &stderr, "crap-mutate")
	if !ok || options.root != "project" || len(options.allowRoots) != 1 {
		t.Fatalf("options = %#v, ok = %v, stderr = %s", options, ok, stderr.String())
	}
	if _, ok := parseMCPOptions([]string{"--unknown"}, &stderr, "crap-mutate"); ok {
		t.Fatal("unknown MCP option was accepted")
	}
}

func TestParseOptionsStopsAtFirstPathAndHonorsDoubleDash(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseOptions([]string{"--language", "go", "src", "--minimum-score", "1"}, &stderr)
	if !ok || options.minimumScore != 80 || len(options.paths) != 3 || options.paths[1] != "--minimum-score" {
		t.Fatalf("options = %#v, ok = %v", options, ok)
	}
	options, ok = parseOptions([]string{"--language", "go", "--minimum-score", "1", "--", "-source"}, &stderr)
	if !ok || options.minimumScore != 1 || len(options.paths) != 1 || options.paths[0] != "-source" {
		t.Fatalf("double-dash options = %#v, ok = %v", options, ok)
	}
}
