package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/mutation"
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
		var stderr bytes.Buffer
		if _, ok := parseOptions(args, &stderr); ok {
			t.Errorf("parseOptions(%v) succeeded", args)
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
