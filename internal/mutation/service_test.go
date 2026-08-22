package mutation

import (
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hbaldwin98/crap/internal/rootauth"
)

type fakeRunner struct {
	t           *testing.T
	wantName    string
	writeReport func(root string, args []string)
	output      string
	result      commandResult
	err         error
}

func (runner fakeRunner) Run(_ context.Context, root, name string, args []string, output io.Writer) (commandResult, error) {
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
	if runner.result.OutputTail == "" {
		runner.result.OutputTail = runner.output
	}
	return runner.result, runner.err
}

func TestServiceRunsGremlinsAndParsesReport(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", writeReport: func(_ string, args []string) {
		if len(args) < 2 || args[1] != "internal" {
			t.Fatalf("args = %#v", args)
		}
		if argumentAfter(t, args, "--threshold-efficacy") != "0" || argumentAfter(t, args, "--threshold-mcover") != "0" {
			t.Fatalf("native thresholds not disabled: %#v", args)
		}
		if argumentAfter(t, args, "--workers") != "1" || argumentAfter(t, args, "--test-cpu") != "1" {
			t.Fatalf("resource limits not applied: %#v", args)
		}
		path := argumentAfter(t, args, "--output")
		writeTestFile(t, path, gremlinsKilledReport)
	}}}
	options := Options{Root: root, Language: "go", Paths: []string{"internal"}, MinimumScore: 80}
	plan, err := service.Plan(options)
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), options, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Engine != "gremlins" || report.Score == nil || *report.Score != 100 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Fingerprints.ConfigSHA256 != plan.Fingerprints.ConfigSHA256 {
		t.Fatalf("report fingerprint %s does not match plan %s", report.Fingerprints.ConfigSHA256, plan.Fingerprints.ConfigSHA256)
	}
}

func TestServiceRejectsUnsafeGoResourceLimits(t *testing.T) {
	root := t.TempDir()
	cases := []Options{
		{Root: root, Language: "go", Workers: -1},
		{Root: root, Language: "go", Workers: MaximumGoMutationParallelism + 1},
		{Root: root, Language: "go", TestCPU: MaximumGoMutationParallelism + 1},
		{Root: root, Language: "go", Workers: 4, TestCPU: 8},
		{Root: root, Language: "typescript", Workers: 1},
	}
	for _, options := range cases {
		if _, err := NewService().Plan(options); err == nil {
			t.Errorf("accepted options %#v", options)
		}
	}
}

func TestServiceRunsStrykerNetWithPaths(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "dotnet", writeReport: func(_ string, args []string) {
		if !strings.Contains(strings.Join(args, " "), "--mutate src/Work.cs") {
			t.Fatalf("args = %#v", args)
		}
		if argumentAfter(t, args, "--break-at") != "0" {
			t.Fatalf("native threshold not disabled: %#v", args)
		}
		output := argumentAfter(t, args, "--output")
		writeTestFile(t, filepath.Join(output, "reports", "mutation-report.json"), `{"schemaVersion":"2","thresholds":{"high":80,"low":60,"break":0},"files":{}}`)
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
		writeTestFile(t, filepath.Join(root, "custom", "mutation.json"), `{"schemaVersion":"1.0","thresholds":{"high":80,"low":60,"break":null},"files":{}}`)
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

func TestServiceRejectsReportAfterEngineFailure(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", err: errors.New("engine failed"), writeReport: func(_ string, args []string) {
		writeTestFile(t, argumentAfter(t, args, "--output"), gremlinsKilledReport)
	}}}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard); err == nil {
		t.Fatal("expected engine failure")
	}
}

func TestServiceAcceptsDocumentedStrykerJSThresholdExit(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{
		t: t, wantName: npxCommand(),
		result: commandResult{ExitCode: 1, OutputTail: "12:34:56 (1234) ERROR MutationTestReportHelper \x1b[31mFinal mutation score 50.00 under breaking threshold 80, setting exit code to 1 (failure).\x1b[39m"},
		writeReport: func(root string, _ []string) {
			writeTestFile(t, filepath.Join(root, "reports", "mutation", "mutation.json"), strykerHalfKilledReport)
		},
	}}
	report, err := service.Run(context.Background(), Options{Root: root, Language: "typescript", MinimumScore: 80}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if report.Provenance.NativeExitCode != 1 {
		t.Fatalf("provenance = %#v", report.Provenance)
	}
}

func TestServiceRejectsStrykerJSThresholdDiagnosticThatDoesNotMatchReport(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{
		t: t, wantName: npxCommand(),
		result: commandResult{ExitCode: 1, OutputTail: "12:34:56 (1234) ERROR MutationTestReportHelper Final mutation score 75.00 under breaking threshold 80, setting exit code to 1 (failure)."},
		writeReport: func(root string, _ []string) {
			writeTestFile(t, filepath.Join(root, "reports", "mutation", "mutation.json"), strykerHalfKilledReport)
		},
	}}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "typescript"}, io.Discard); err == nil {
		t.Fatal("expected mismatched threshold diagnostic failure")
	}
}

func TestServiceRejectsOtherStrykerJSExitOne(t *testing.T) {
	root := t.TempDir()
	service := &Service{runner: fakeRunner{
		t: t, wantName: npxCommand(), result: commandResult{ExitCode: 1, OutputTail: "test runner crashed"},
		writeReport: func(root string, _ []string) {
			writeTestFile(t, filepath.Join(root, "reports", "mutation", "mutation.json"), strykerHalfKilledReport)
		},
	}}
	if _, err := service.Run(context.Background(), Options{Root: root, Language: "typescript"}, io.Discard); err == nil {
		t.Fatal("expected native process failure")
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
		writeTestFile(t, argumentAfter(t, args, "--output"), gremlinsKilledReport)
	}}}
	if _, err := service.Run(ctx, Options{Root: root, Language: "go", MinimumScore: 80}, io.Discard); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("error = %v", err)
	}
}

const gremlinsKilledReport = `{"test_efficacy":100,"mutants_total":1,"mutants_killed":1,"mutants_lived":0,"mutants_not_viable":0,"mutants_not_covered":0,"files":[{"file_name":"work.go","mutations":[{"line":1,"column":1,"type":"CONDITIONALS_NEGATION","status":"KILLED"}]}]}`

const strykerHalfKilledReport = `{"schemaVersion":"1.0","thresholds":{"high":90,"low":80,"break":80},"files":{"work.ts":{"language":"typescript","source":"return true;","mutants":[{"id":"1","mutatorName":"x","status":"Killed","location":{"start":{"line":1,"column":1},"end":{"line":1,"column":2}}},{"id":"2","mutatorName":"x","status":"Survived","location":{"start":{"line":2,"column":1},"end":{"line":2,"column":2}}}]}}}`

func TestCommandErrorDistinguishesDeadlineAndCancellation(t *testing.T) {
	deadlineContext, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if err := commandError(deadlineContext, errors.New("engine stopped")); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("deadline error = %v", err)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := commandError(canceledContext, errors.New("engine stopped")); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestNpxCommandMatchesPlatform(t *testing.T) {
	want := "npx"
	if runtime.GOOS == "windows" {
		want = "npx.cmd"
	}
	if command := npxCommand(); command != want {
		t.Fatalf("npxCommand() = %q, want %q", command, want)
	}
}

func TestServiceRejectsUnsafeGoPathsAndIncrementalMode(t *testing.T) {
	root := t.TempDir()
	service := NewService()
	outside := filepath.Dir(root)
	for _, paths := range [][]string{{"one", "two"}, {".."}, {outside}, {"missing"}} {
		if _, err := service.Run(context.Background(), Options{Root: root, Language: "go", Paths: paths}, io.Discard); err == nil {
			t.Errorf("expected Go path error for %#v", paths)
		}
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

func TestValidateAuthorizedMutationPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := policy.Root("")
	if err != nil {
		t.Fatal(err)
	}
	valid := Options{Root: root, Language: "typescript", Paths: []string{"src/**/*.ts"}, MinimumScore: 80, Authorization: scope}
	if err := validate(&valid); err != nil {
		t.Fatalf("valid pattern rejected: %v", err)
	}
	missing := Options{Root: root, Language: "typescript", MinimumScore: 80, Authorization: scope}
	if err := validate(&missing); err == nil {
		t.Fatal("authorized TypeScript run without explicit paths was accepted")
	}
	for _, pattern := range []string{"../outside/**/*.ts", filepath.Join(t.TempDir(), "*.ts")} {
		options := Options{Root: root, Language: "typescript", Paths: []string{pattern}, MinimumScore: 80, Authorization: scope}
		if err := validate(&options); err == nil {
			t.Errorf("unsafe pattern %q was accepted", pattern)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.ts")
	writeTestFile(t, outside, "export const outside = true")
	link := filepath.Join(root, "src", "outside.ts")
	if err := os.Symlink(outside, link); err == nil {
		for _, pattern := range []string{"src/outside.ts", "src/**/*.ts"} {
			options := Options{Root: root, Language: "typescript", Paths: []string{pattern}, MinimumScore: 80, Authorization: scope}
			if err := validate(&options); err == nil {
				t.Errorf("symlinked pattern %q was accepted", pattern)
			}
		}
	}
}
