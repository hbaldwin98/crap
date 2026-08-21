package mutation

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	t           *testing.T
	wantName    string
	writeReport func(root string, args []string)
	output      string
	err         error
}

func (runner fakeRunner) Run(_ context.Context, root, name string, args []string, output io.Writer) error {
	runner.t.Helper()
	if name != runner.wantName {
		runner.t.Fatalf("command = %q, want %q", name, runner.wantName)
	}
	if runner.writeReport != nil {
		runner.writeReport(root, args)
	}
	if runner.output != "" {
		_, _ = io.WriteString(output, runner.output)
	}
	return runner.err
}

func TestServiceRunsGremlinsAndParsesReport(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", writeReport: func(_ string, args []string) {
		path := argumentAfter(t, args, "--output")
		writeTestFile(t, path, `{"test_efficacy":100,"files":[]}`)
	}}}
	report, err := service.Run(context.Background(), Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Engine != "gremlins" || report.Score == nil || *report.Score != 100 {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestServiceRunsStrykerNetWithPaths(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "dotnet", writeReport: func(_ string, args []string) {
		if !strings.Contains(strings.Join(args, " "), "--mutate src/Work.cs") {
			t.Fatalf("args = %#v", args)
		}
		output := argumentAfter(t, args, "--output")
		writeTestFile(t, filepath.Join(output, "reports", "mutation-report.json"), `{"schemaVersion":"1.0","files":{}}`)
	}}}
	report, err := service.Run(context.Background(), Options{Root: root, Language: "csharp", Paths: []string{"src/Work.cs"}, MinimumScore: 80}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Engine != "stryker-net" {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestServiceRunsStrykerJSWithCustomReport(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: npxCommand(), writeReport: func(root string, args []string) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--incremental") || argumentAfter(t, args, "--mutate") != "src/work.ts,src/other.ts" {
			t.Fatalf("args = %#v", args)
		}
		writeTestFile(t, filepath.Join(root, "custom", "mutation.json"), `{"schemaVersion":"1.0","files":{}}`)
	}}}
	report, err := service.Run(context.Background(), Options{
		Root: root, Language: "typescript", Paths: []string{"src/work.ts", "src/other.ts"}, MinimumScore: 80,
		Incremental: true, ReportPath: "custom/mutation.json",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Engine != "stryker-js" {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestServiceRejectsUnchangedStrykerJSReport(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "reports", "mutation", "mutation.json")
	writeTestFile(t, reportPath, `{"schemaVersion":"1.0","files":{}}`)
	service := &Service{runner: fakeRunner{t: t, wantName: npxCommand(), writeReport: func(string, []string) {}}}
	_, err := service.Run(context.Background(), Options{Root: root, Language: "typescript", MinimumScore: 80}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "did not update") {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceDoesNotAcceptReportAfterEngineFailure(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", err: errors.New("engine failed"), writeReport: func(_ string, args []string) {
		writeTestFile(t, argumentAfter(t, args, "--output"), `{"test_efficacy":100,"files":[]}`)
	}}}
	report, err := service.Run(context.Background(), Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard)
	if err != nil || report.Score == nil || *report.Score != 100 {
		t.Fatalf("report = %#v, error = %v", report, err)
	}
}

func TestServiceHandlesGremlinsRunWithoutMutants(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", output: "\nNo results to report.\n"}}
	report, err := service.Run(context.Background(), Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Score != nil || report.Passed || report.Summary.Total != 0 || report.ScoreSource != "unavailable" {
		t.Fatalf("report = %#v", report)
	}
}

func TestServiceRejectsMissingGremlinsReportWithoutNoResultsMessage(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", output: "Error: No results to report. Output failed.\n"}}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard); err == nil {
		t.Fatal("expected missing report error")
	}
}

func TestServiceRejectsReportPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.json")
	service := &Service{runner: fakeRunner{t: t, wantName: npxCommand()}}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "typescript", ReportPath: outside}, io.Discard); err == nil {
		t.Fatal("expected report path error")
	}
}

func TestServiceDoesNotAcceptReportAfterCancellation(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", err: errors.New("engine failed"), writeReport: func(_ string, args []string) {
		writeTestFile(t, argumentAfter(t, args, "--output"), `{"test_efficacy":100,"files":[]}`)
	}}}
	if _, err := service.Run(ctx, Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("error = %v", err)
	}
}

func TestServiceRejectsUnsupportedGoPathAndIncrementalMode(t *testing.T) {
	root := t.TempDir()
	service := NewService()
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "go", Paths: []string{"internal"}}, io.Discard); err == nil {
		t.Fatal("expected Go path error")
	}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "csharp", Incremental: true}, io.Discard); err == nil {
		t.Fatal("expected incremental mode error")
	}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "go", MinimumScore: math.NaN()}, io.Discard); err == nil {
		t.Fatal("expected non-finite minimum score error")
	}
}

func argumentAfter(t *testing.T, args []string, name string) string {
	t.Helper()
	for index := range args {
		if args[index] == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("%s not found in %#v", name, args)
	return ""
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
