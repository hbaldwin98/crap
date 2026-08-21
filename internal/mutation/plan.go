package mutation

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const temporaryReportPath = "$REPORT_PATH"

func (service *Service) Plan(options Options) (Plan, error) {
	if err := validate(&options); err != nil {
		return Plan{}, err
	}
	reportPath := temporaryReportPath
	if options.Language == "typescript" {
		var err error
		reportPath, err = resolveReportPath(options.Root, options.ReportPath, options.Authorization)
		if err != nil {
			return Plan{}, err
		}
	}
	executable, arguments, reportPath := commandPlan(options, reportPath)
	return Plan{
		SchemaVersion: PlanSchemaVersion, Root: options.Root, Language: options.Language,
		Engine: engineName(options.Language), Executable: executable, Arguments: arguments,
		ReportPath: reportPath, TimeoutSeconds: options.TimeoutSeconds, MinimumScore: options.MinimumScore,
	}, nil
}

func (service *Service) Doctor(ctx context.Context, options Options) (DoctorReport, error) {
	plan, err := service.Plan(options)
	if err != nil {
		return DoctorReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(plan.TimeoutSeconds)*time.Second)
	defer cancel()
	report := DoctorReport{SchemaVersion: PlanSchemaVersion, Plan: plan, Ready: true}
	lookPath := service.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath(plan.Executable); err != nil {
		report.Checks = append(report.Checks, DoctorCheck{Name: "executable", Status: "error", Message: err.Error()})
		report.Ready = false
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "executable", Status: "passed", Message: plan.Executable + " is available"})
	}
	marker := projectMarker(plan.Root, plan.Language)
	if marker == "" {
		report.Checks = append(report.Checks, DoctorCheck{Name: "project", Status: "warning", Message: "no recognized project marker found at the project root"})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{Name: "project", Status: "passed", Message: marker})
	}
	if report.Ready {
		name, args := versionCommand(plan.Language)
		result, runErr := service.runner.Run(ctx, plan.Root, name, args, io.Discard)
		if ctx.Err() != nil || runErr != nil || result.ExitCode != 0 {
			message := result.OutputTail
			if runErr != nil {
				message = runErr.Error()
			}
			if ctx.Err() != nil {
				message = ctx.Err().Error()
			}
			report.Checks = append(report.Checks, DoctorCheck{Name: "engine-version", Status: "error", Message: strings.TrimSpace(message)})
			report.Ready = false
		} else {
			report.Checks = append(report.Checks, DoctorCheck{Name: "engine-version", Status: "passed", Message: strings.TrimSpace(result.OutputTail)})
			report.Checks = append(report.Checks, DoctorCheck{Name: "compatibility", Status: "warning", Message: compatibilityMessage(plan.Language)})
		}
	}
	return report, nil
}

func compatibilityMessage(language string) string {
	switch language {
	case "go":
		return "engine version was reported; Gremlins v0.6 report compatibility is enforced when a run is parsed"
	case "csharp":
		return "engine version was reported; Stryker.NET schema 2 compatibility is enforced when a run is parsed"
	default:
		return "engine version was reported; StrykerJS schema 1.0 compatibility is enforced when a run is parsed"
	}
}

func commandPlan(options Options, reportPath string) (string, []string, string) {
	switch options.Language {
	case "go":
		args := []string{"unleash"}
		if len(options.Paths) == 1 {
			args = append(args, options.Paths[0])
		}
		args = append(args, "--output", reportPath, "--threshold-efficacy", "0", "--threshold-mcover", "0")
		return "gremlins", args, reportPath
	case "csharp":
		args := []string{"stryker", "--reporter", "json", "--output", reportPath, "--break-at", "0"}
		for _, path := range options.Paths {
			args = append(args, "--mutate", filepath.ToSlash(path))
		}
		return "dotnet", args, reportPath
	default:
		args := []string{"--no-install", "stryker", "run", "--reporters", "json"}
		if options.Incremental {
			args = append(args, "--incremental")
		}
		if len(options.Paths) > 0 {
			args = append(args, "--mutate", strings.Join(options.Paths, ","))
		}
		return npxCommand(), args, reportPath
	}
}

func versionCommand(language string) (string, []string) {
	switch language {
	case "go":
		return "gremlins", []string{"--version"}
	case "csharp":
		return "dotnet", []string{"stryker", "--version"}
	default:
		return npxCommand(), []string{"--no-install", "stryker", "--version"}
	}
}

func engineName(language string) string {
	switch language {
	case "go":
		return "gremlins"
	case "csharp":
		return "stryker-net"
	default:
		return "stryker-js"
	}
}

func projectMarker(root, language string) string {
	patterns := map[string][]string{
		"go":         {"go.mod"},
		"csharp":     {"*.sln", "*.csproj"},
		"typescript": {"package.json"},
	}[language]
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		if len(matches) > 0 {
			return fmt.Sprintf("found %s", filepath.Base(matches[0]))
		}
	}
	return ""
}

func replaceReportPath(arguments []string, reportPath string) []string {
	result := append([]string(nil), arguments...)
	for index := range result {
		if result[index] == temporaryReportPath {
			result[index] = reportPath
		}
	}
	return result
}
