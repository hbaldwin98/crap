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

func TestRunCallsHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"calls", "--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunCallsJSONReportsModuleEdges(t *testing.T) {
	writeCallsFixture(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"calls", "--format", "json", "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var report struct {
		ReportType    string `json:"reportType"`
		SchemaVersion string `json:"schemaVersion"`
		Module        struct {
			Path string `json:"path"`
		} `json:"module"`
		Summary struct {
			Packages        int `json:"packages"`
			Functions       int `json:"functions"`
			Edges           int `json:"edges"`
			StaticEdges     int `json:"staticEdges"`
			DispatchEdges   int `json:"dispatchEdges"`
			UnresolvedCalls int `json:"unresolvedCalls"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ReportType != "call-graph" || report.SchemaVersion != "1" {
		t.Fatalf("report = %#v", report)
	}
	if report.Module.Path != "example.com/calls" {
		t.Fatalf("module = %#v", report.Module)
	}
	if report.Summary.Functions == 0 || report.Summary.Edges == 0 || report.Summary.Packages == 0 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if report.Summary.StaticEdges+report.Summary.DispatchEdges != report.Summary.Edges {
		t.Fatalf("edge kinds do not sum: %#v", report.Summary)
	}
}

func TestRunCallsTextSummarizesGraph(t *testing.T) {
	writeCallsFixture(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"calls", "."}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "functions, ") || !strings.Contains(got, "tests, ") || !strings.Contains(got, "unresolved calls") {
		t.Fatalf("stdout = %q", got)
	}
	if !strings.Contains(got, "limitation: ") {
		t.Fatalf("stdout missing limitations: %q", got)
	}
}

func TestRunCallsRequiresGoModule(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"calls", "--format", "json", "."}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no go.mod found") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCallsUnsupportedFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"calls", "--format", "xml", "."}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
}

func writeCallsFixture(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/calls\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "package calls\n\nfunc Run() int {\n\treturn Helper()\n}\n\nfunc Helper() int {\n\treturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	testSource := "package calls\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {\n\tif Run() != 1 {\n\t\tt.Fatal(\"unexpected\")\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "work_test.go"), []byte(testSource), 0o600); err != nil {
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
}

func TestWriteCallsText(t *testing.T) {
	report := analysis.CallGraphReport{
		Summary: analysis.CallGraphSummary{
			Packages: 2, Functions: 12, Tests: 2, Edges: 9,
			StaticEdges: 7, DispatchEdges: 2, UnresolvedCalls: 9,
			ChangedCallables: 1, AffectedTests: 2,
		},
		AffectedTests: []analysis.CallGraphAffectedTest{
			{Name: "TestRun", File: "work_test.go", Distance: 1, Seeds: []string{"a", "b"}},
			{Name: "TestIndirect", File: "other_test.go", Distance: 2, Seeds: []string{"a"}},
		},
		Limitations: []string{"module-scoped resolution"},
	}
	var buf bytes.Buffer
	if err := writeCallsText(&buf, report); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"12 functions, 9 edges (7 static, 2 dispatch) across 2 packages\n",
		"2 tests, 1 changed callables, 2 affected tests, 9 unresolved calls\n",
		"AFFECTED TestRun work_test.go distance 1 seeds a,b\n",
		"AFFECTED TestIndirect other_test.go distance 2 seeds a\n",
		"limitation: module-scoped resolution\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}
