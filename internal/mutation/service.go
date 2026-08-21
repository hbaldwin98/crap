package mutation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Service struct{ runner commandRunner }

func NewService() *Service { return &Service{runner: execRunner{}} }

func (service *Service) Run(ctx context.Context, options Options, output io.Writer) (Report, error) {
	if output == nil {
		output = io.Discard
	}
	if err := validate(&options); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(options.TimeoutSeconds)*time.Second)
	defer cancel()
	switch options.Language {
	case "csharp":
		return service.runStrykerNet(ctx, options, output)
	case "go":
		return service.runGremlins(ctx, options, output)
	case "typescript":
		return service.runStrykerJS(ctx, options, output)
	default:
		return Report{}, fmt.Errorf("unsupported language %q", options.Language)
	}
}

func validate(options *Options) error {
	if options.Root == "" {
		root, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine working directory: %w", err)
		}
		options.Root = root
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("read root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("root is not a directory: %s", root)
	}
	options.Root = root
	options.Language = strings.ToLower(options.Language)
	if math.IsNaN(options.MinimumScore) || math.IsInf(options.MinimumScore, 0) || options.MinimumScore < 0 || options.MinimumScore > 100 {
		return fmt.Errorf("minimum score must be between 0 and 100")
	}
	if options.TimeoutSeconds == 0 {
		options.TimeoutSeconds = 1800
	}
	if options.TimeoutSeconds < 1 {
		return fmt.Errorf("timeout must be positive")
	}
	if options.Language == "go" {
		for _, path := range options.Paths {
			if path != "" && path != "." {
				return fmt.Errorf("Gremlins does not support include paths; use a whole-module run or its project configuration")
			}
		}
	}
	if options.Incremental && options.Language != "typescript" {
		return fmt.Errorf("incremental mode is only supported for TypeScript")
	}
	return nil
}

func (service *Service) runGremlins(ctx context.Context, options Options, output io.Writer) (Report, error) {
	directory, err := os.MkdirTemp("", "crap-mutate-gremlins-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(directory)
	reportPath := filepath.Join(directory, "mutation.json")
	engineOutput := &tailBuffer{limit: capturedOutputLimit}
	runErr := service.runner.Run(ctx, options.Root, "gremlins", []string{"unleash", "--output", reportPath}, io.MultiWriter(output, engineOutput))
	if ctx.Err() != nil {
		return Report{}, commandError(ctx, runErr)
	}
	data, err := os.ReadFile(reportPath)
	if os.IsNotExist(err) {
		if runErr == nil && outputHasLine(engineOutput.String(), "No results to report.") {
			return makeReport("go", "gremlins", "unavailable", options.MinimumScore, nil, []MutantResult{}), nil
		}
		if runErr != nil {
			return Report{}, commandError(ctx, runErr)
		}
		return Report{}, fmt.Errorf("Gremlins completed without a JSON report")
	}
	if err != nil {
		return Report{}, fmt.Errorf("read Gremlins report: %w", err)
	}
	return parseGremlins(data, options.MinimumScore)
}

func outputHasLine(output, expected string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}

func (service *Service) runStrykerNet(ctx context.Context, options Options, output io.Writer) (Report, error) {
	directory, err := os.MkdirTemp("", "crap-mutate-stryker-net-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(directory)
	args := []string{"stryker", "--reporter", "json", "--output", directory}
	for _, path := range options.Paths {
		args = append(args, "--mutate", filepath.ToSlash(path))
	}
	runErr := service.runner.Run(ctx, options.Root, "dotnet", args, output)
	if ctx.Err() != nil {
		return Report{}, commandError(ctx, runErr)
	}
	reportPath, findErr := findJSONReport(directory)
	if findErr != nil {
		if runErr != nil {
			return Report{}, commandError(ctx, runErr)
		}
		return Report{}, findErr
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return Report{}, fmt.Errorf("read Stryker.NET report: %w", err)
	}
	report, err := parseStryker(data, "csharp", "stryker-net", options.MinimumScore)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

func (service *Service) runStrykerJS(ctx context.Context, options Options, output io.Writer) (Report, error) {
	reportPath, err := resolveReportPath(options.Root, options.ReportPath)
	if err != nil {
		return Report{}, err
	}
	args := []string{"--no-install", "stryker", "run", "--reporters", "json"}
	if options.Incremental {
		args = append(args, "--incremental")
	}
	if len(options.Paths) > 0 {
		args = append(args, "--mutate", strings.Join(options.Paths, ","))
	}
	previous, err := reportState(reportPath)
	if err != nil {
		return Report{}, err
	}
	runErr := service.runner.Run(ctx, options.Root, npxCommand(), args, output)
	if ctx.Err() != nil {
		return Report{}, commandError(ctx, runErr)
	}
	current, stateErr := reportState(reportPath)
	if stateErr != nil {
		return Report{}, stateErr
	}
	if !current.exists || current == previous {
		if runErr != nil {
			return Report{}, commandError(ctx, runErr)
		}
		return Report{}, fmt.Errorf("StrykerJS did not update JSON report %s", reportPath)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return Report{}, fmt.Errorf("read StrykerJS report %s: %w", reportPath, err)
	}
	report, err := parseStryker(data, "typescript", "stryker-js", options.MinimumScore)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

type fileState struct {
	exists  bool
	size    int64
	modTime time.Time
	digest  [sha256.Size]byte
}

func resolveReportPath(root, configured string) (string, error) {
	if configured == "" {
		configured = filepath.Join("reports", "mutation", "mutation.json")
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("StrykerJS report path must be inside the project root")
	}
	if !strings.EqualFold(filepath.Ext(path), ".json") {
		return "", fmt.Errorf("StrykerJS report path must be a JSON file")
	}
	return path, nil
}

func reportState(path string) (fileState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fileState{}, nil
	}
	if err != nil {
		return fileState{}, fmt.Errorf("inspect mutation report %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileState{}, fmt.Errorf("inspect mutation report %s: %w", path, err)
	}
	return fileState{exists: true, size: info.Size(), modTime: info.ModTime(), digest: sha256.Sum256(data)}, nil
}

func findJSONReport(root string) (string, error) {
	var reports []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
			reports = append(reports, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find Stryker.NET report: %w", err)
	}
	if len(reports) != 1 {
		return "", fmt.Errorf("expected one Stryker.NET JSON report, found %d", len(reports))
	}
	return reports[0], nil
}

func commandError(ctx context.Context, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("mutation run timed out: %w", ctx.Err())
	}
	if ctx.Err() != nil {
		return fmt.Errorf("mutation run canceled: %w", ctx.Err())
	}
	return err
}

func npxCommand() string {
	if runtime.GOOS == "windows" {
		return "npx.cmd"
	}
	return "npx"
}
