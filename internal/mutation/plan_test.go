package mutation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanBuildsExactEngineCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		options Options
		command string
		args    []string
	}{
		{name: "go", options: Options{Root: root, Language: "go", Paths: []string{"internal"}}, command: "gremlins", args: []string{"unleash", "internal", "--output", "$REPORT_PATH", "--threshold-efficacy", "0", "--threshold-mcover", "0"}},
		{name: "csharp", options: Options{Root: root, Language: "csharp", Paths: []string{"src/Work.cs"}}, command: "dotnet", args: []string{"stryker", "--reporter", "json", "--output", "$REPORT_PATH", "--break-at", "0", "--mutate", "src/Work.cs"}},
		{name: "typescript", options: Options{Root: root, Language: "typescript", Paths: []string{"src/**/*.ts", "!src/**/*.spec.ts"}, Incremental: true}, command: npxCommand(), args: []string{"--no-install", "stryker", "run", "--reporters", "json", "--incremental", "--mutate", "src/**/*.ts,!src/**/*.spec.ts"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := NewService().Plan(test.options)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Executable != test.command || !reflect.DeepEqual(plan.Arguments, test.args) || plan.SchemaVersion != PlanSchemaVersion {
				t.Fatalf("plan = %#v", plan)
			}
			if test.name == "typescript" && plan.ReportPath != filepath.Join(root, "reports", "mutation", "mutation.json") {
				t.Fatalf("report path = %q", plan.ReportPath)
			}
		})
	}
}

func TestPlanRejectsUnsupportedLanguage(t *testing.T) {
	if _, err := NewService().Plan(Options{Root: t.TempDir(), Language: "rust"}); err == nil {
		t.Fatal("unsupported language was accepted")
	}
}

func TestDoctorReportsVersionAndProjectChecks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{runner: fakeRunner{t: t, wantName: "gremlins", output: "gremlins version v0.6.0\n"}, lookPath: func(name string) (string, error) { return name, nil }}
	report, err := service.Doctor(context.Background(), Options{Root: root, Language: "go", TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Checks) != 4 || report.Checks[2].Message != "gremlins version v0.6.0" {
		t.Fatalf("doctor = %#v", report)
	}
}

func TestDoctorReportsMissingExecutableWithoutRunningVersion(t *testing.T) {
	service := &Service{
		runner:   fakeRunner{t: t, wantName: "must-not-run"},
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	report, err := service.Doctor(context.Background(), Options{Root: t.TempDir(), Language: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || len(report.Checks) != 2 || report.Checks[0].Status != "error" {
		t.Fatalf("doctor = %#v", report)
	}
}
