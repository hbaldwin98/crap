package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
)

func TestRunArchitectureHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunArchitectureJSONReportsAcyclic(t *testing.T) {
	root := writeArchitectureFixture(t, "package alpha\nfunc Run() {}\n")

	var stdout, stderr bytes.Buffer
	// JSON output with a path that produces a code graph containing modules.
	if code := run([]string{"arch", "--format", "json", "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var report struct {
		ReportType string `json:"reportType"`
		Summary    struct {
			Modules    int  `json:"modules"`
			Edges      int  `json:"edges"`
			Cycles     int  `json:"cycles"`
			Violations int  `json:"violations"`
			Complete   bool `json:"complete"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ReportType != "architecture" {
		t.Fatalf("report = %#v", report)
	}
	if !report.Summary.Complete {
		t.Fatalf("expected complete, got %#v", report.Summary)
	}
	_ = root
}

func TestRunArchitectureExitCodeOnViolation(t *testing.T) {
	root := writeArchitectureFixture(t, "package alpha\nimport _ \"example.com/app/dep\"\nfunc Run() {}\n")
	if err := os.MkdirAll(filepath.Join(root, "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dep", "dep.go"), []byte("package dep\nfunc Dep() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rulesPath := filepath.Join(root, "rules.json")
	if err := os.WriteFile(rulesPath, []byte(`{"forbid":[{"from":"**","to":"**"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "--format", "json", "--rules", rulesPath, "."}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, stderr = %s, stdout = %s", code, stderr.String(), stdout.String())
	}
}

func TestRunArchitectureMissingRulesFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "--rules", "nope.json", "."}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

func TestRunArchitectureUnsupportedFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"arch", "--format", "xml", "."}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

// writeArchitectureFixture changes into a temp Go module directory and returns
// its path. It must be run before any chdir-based test helpers that rely on the
// working directory.
func writeArchitectureFixture(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	return root
}

func TestWriteArchitectureText(t *testing.T) {
	report := analysis.ArchitectureReport{
		Summary: analysis.ArchitectureSummary{Modules: 3, Edges: 2, Cycles: 1, Violations: 2},
		Cycles: []analysis.ArchitectureCycle{
			{Modules: []string{"a", "b"}, Edges: []analysis.ArchitectureEdgeReference{
				{From: "a", To: "b"},
				{From: "b", To: "a"},
			}},
		},
		Violations: []analysis.ArchitectureViolation{
			{Kind: "forbid", From: "a", To: "b", Reason: "layering"},
			{Kind: "cycle", From: "b", To: "a"},
			{Kind: "forbid", From: "c", To: "d"},
		},
		Limitations: []string{"baseline missing"},
	}
	var buf bytes.Buffer
	writeArchitectureText(&buf, report)
	got := buf.String()
	for _, want := range []string{
		"3 modules, 2 dependencies, 1 cycles, 2 violations\n",
		"CYCLE a -> b\n",
		"  b -> a\n",
		"VIOLATION forbid a -> b (layering)\n",
		"VIOLATION cycle b -> a (cycle)\n",
		"VIOLATION forbid c -> d\n",
		"LIMITATION baseline missing\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
