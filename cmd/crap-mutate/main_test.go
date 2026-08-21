package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/mutation"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, stderr.String())
	}
	if stdout.String() != version+"\n" {
		t.Fatalf("version = %q", stdout.String())
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
