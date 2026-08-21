package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/buildinfo"
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

func TestRunRejectsInvalidOptions(t *testing.T) {
	tests := [][]string{
		{"--format", "yaml"},
		{"--threshold", "-1"},
		{"--not-a-flag"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Errorf("run(%v) exit code = %d, want 1", args, code)
		}
	}
}

func TestParseOptionsAcceptsStrictCoverage(t *testing.T) {
	var stderr bytes.Buffer
	options, ok := parseOptions([]string{"--strict-coverage", "src"}, &stderr)
	if !ok || !options.strictCoverage || len(options.paths) != 1 || options.paths[0] != "src" {
		t.Fatalf("options = %#v, ok = %v, stderr = %s", options, ok, stderr.String())
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
