package mutation

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/hbaldwin98/crap/internal/rootauth"
)

type Service struct {
	runner   commandRunner
	lookPath func(string) (string, error)
}

func NewService() *Service { return &Service{runner: execRunner{}, lookPath: exec.LookPath} }

func (service *Service) Run(ctx context.Context, options Options, output io.Writer) (Report, error) {
	if output == nil {
		output = io.Discard
	}
	if err := validate(&options); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(options.TimeoutSeconds)*time.Second)
	defer cancel()
	var report Report
	var err error
	switch options.Language {
	case "csharp":
		report, err = service.runStrykerNet(ctx, options, output)
	case "go":
		report, err = service.runGremlins(ctx, options, output)
	case "typescript":
		report, err = service.runStrykerJS(ctx, options, output)
	default:
		return Report{}, fmt.Errorf("unsupported language %q", options.Language)
	}
	if err != nil {
		return Report{}, err
	}
	report.Fingerprints.ConfigSHA256 = mutationConfigFingerprint(options)
	return report, nil
}

func validate(options *Options) error {
	if err := validateRoot(options); err != nil {
		return err
	}
	if err := validateMutationBasics(options); err != nil {
		return err
	}
	if err := validateResourceLimits(options); err != nil {
		return err
	}
	if err := validateMutationPaths(options); err != nil {
		return err
	}
	if options.Incremental && options.Language != "typescript" {
		return fmt.Errorf("incremental mode is only supported for TypeScript")
	}
	return nil
}

func validateMutationBasics(options *Options) error {
	options.Language = strings.ToLower(options.Language)
	if options.Language != "csharp" && options.Language != "go" && options.Language != "typescript" {
		return fmt.Errorf("unsupported language %q", options.Language)
	}
	if math.IsNaN(options.MinimumScore) || math.IsInf(options.MinimumScore, 0) || options.MinimumScore < 0 || options.MinimumScore > 100 {
		return fmt.Errorf("minimum score must be between 0 and 100")
	}
	if options.TimeoutSeconds == 0 {
		options.TimeoutSeconds = 1800
	}
	if options.TimeoutSeconds < 1 {
		return fmt.Errorf("timeout must be positive")
	}
	return nil
}

func validateResourceLimits(options *Options) error {
	if options.Language != "go" {
		if options.Workers != 0 || options.TestCPU != 0 {
			return fmt.Errorf("workers and test CPU limits are only supported for Go")
		}
		return nil
	}
	if options.Workers == 0 {
		options.Workers = DefaultGoMutationWorkers
	}
	if options.TestCPU == 0 {
		options.TestCPU = DefaultGoMutationTestCPU
	}
	if options.Workers < 1 || options.Workers > MaximumGoMutationParallelism {
		return fmt.Errorf("workers must be between 1 and %d", MaximumGoMutationParallelism)
	}
	if options.TestCPU < 1 || options.TestCPU > MaximumGoMutationParallelism {
		return fmt.Errorf("test CPU must be between 1 and %d", MaximumGoMutationParallelism)
	}
	if options.Workers*options.TestCPU > MaximumGoMutationParallelism {
		return fmt.Errorf("workers multiplied by test CPU must not exceed %d", MaximumGoMutationParallelism)
	}
	return nil
}

func validateRoot(options *Options) error {
	if options.Root == "" {
		root, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determine working directory: %w", err)
		}
		options.Root = root
	}
	if options.Authorization != nil {
		options.Root = options.Authorization.Path()
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
	return nil
}

func validateMutationPaths(options *Options) error {
	if options.Language == "go" {
		if len(options.Paths) > 1 {
			return fmt.Errorf("Gremlins accepts one package directory per run")
		}
		if len(options.Paths) == 1 {
			path, err := packagePath(options.Root, options.Paths[0], options.Authorization)
			if err != nil {
				return err
			}
			options.Paths[0] = path
		}
	}
	if options.Authorization != nil && (options.Language == "csharp" || options.Language == "typescript") {
		if len(options.Paths) == 0 {
			return fmt.Errorf("authorized C# and TypeScript mutation runs require explicit paths")
		}
		for _, pattern := range options.Paths {
			if err := authorizeMutationPattern(options.Authorization, pattern); err != nil {
				return err
			}
		}
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
	name, plannedArgs, _ := commandPlan(options, temporaryReportPath)
	args := replaceReportPath(plannedArgs, reportPath)
	result, runErr := service.runner.Run(ctx, options.Root, name, args, io.MultiWriter(output, engineOutput))
	if ctx.Err() != nil {
		return Report{}, commandError(ctx, runErr)
	}
	if runErr != nil {
		return Report{}, commandError(ctx, runErr)
	}
	if result.ExitCode != 0 {
		return Report{}, nativeCommandError("gremlins", result)
	}
	data, err := os.ReadFile(reportPath)
	if os.IsNotExist(err) {
		if outputHasLine(engineOutput.String(), "No results to report.") {
			return makeReport("go", "gremlins", "unavailable", options.MinimumScore, nil, []MutantResult{}, Provenance{NativeExitCode: 0}), nil
		}
		return Report{}, fmt.Errorf("Gremlins completed without a JSON report")
	}
	if err != nil {
		return Report{}, fmt.Errorf("read Gremlins report: %w", err)
	}
	return parseGremlins(data, options.MinimumScore)
}

func packagePath(root, path string, authorization *rootauth.Root) (string, error) {
	if path == "" {
		path = "."
	}
	absolute, err := filepath.Abs(filepath.Join(root, path))
	if err != nil {
		return "", fmt.Errorf("resolve Go package path: %w", err)
	}
	if authorization != nil {
		absolute, err = authorization.Existing(absolute)
		if err != nil {
			return "", fmt.Errorf("authorize Go package path: %w", err)
		}
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Go package path must be within root")
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("read Go package path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("Go package path is not a directory: %s", path)
	}
	return filepath.ToSlash(relative), nil
}

func authorizeMutationPattern(authorization *rootauth.Root, pattern string) error {
	normalized, err := normalizeMutationPattern(pattern)
	if err != nil {
		return err
	}
	meta := strings.IndexAny(normalized, "*?[{")
	if meta < 0 {
		return authorizeExistingMutationPath(authorization, pattern, normalized)
	}
	return authorizeMutationGlobPrefix(authorization, pattern, normalized[:meta])
}

func normalizeMutationPattern(pattern string) (string, error) {
	trimmed := strings.TrimPrefix(pattern, "!")
	if trimmed == "" || filepath.IsAbs(trimmed) || filepath.VolumeName(trimmed) != "" {
		return "", fmt.Errorf("mutation path must be a relative pattern within root: %s", pattern)
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	if strings.ContainsAny(normalized, "{}") {
		return "", fmt.Errorf("brace expansion is not allowed in authorized mutation paths: %s", pattern)
	}
	for _, component := range strings.Split(normalized, "/") {
		if component == ".." {
			return "", fmt.Errorf("mutation path must not escape root: %s", pattern)
		}
	}
	return normalized, nil
}

func authorizeExistingMutationPath(authorization *rootauth.Root, pattern, normalized string) error {
	if _, err := authorization.Existing(filepath.FromSlash(normalized)); err != nil {
		return fmt.Errorf("authorize mutation path %q: %w", pattern, err)
	}
	return nil
}

func authorizeMutationGlobPrefix(authorization *rootauth.Root, pattern, globPrefix string) error {
	prefix := globPrefix
	prefixEndedAtSeparator := strings.HasSuffix(prefix, "/")
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		prefix = "."
	} else if !prefixEndedAtSeparator {
		prefix = filepath.Dir(filepath.FromSlash(prefix))
	}
	authorizedPrefix, err := authorization.Existing(filepath.FromSlash(prefix))
	if err != nil {
		return fmt.Errorf("authorize mutation path %q: %w", pattern, err)
	}
	if err := rejectSymlinks(authorizedPrefix); err != nil {
		return fmt.Errorf("authorize mutation path %q: %w", pattern, err)
	}
	return nil
}

func rejectSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("wildcard scope contains symlink %s", path)
		}
		return nil
	})
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
	name, plannedArgs, _ := commandPlan(options, temporaryReportPath)
	args := replaceReportPath(plannedArgs, directory)
	result, runErr := service.runner.Run(ctx, options.Root, name, args, output)
	if ctx.Err() != nil {
		return Report{}, commandError(ctx, runErr)
	}
	if runErr != nil {
		return Report{}, commandError(ctx, runErr)
	}
	if result.ExitCode != 0 {
		return Report{}, nativeCommandError("dotnet", result)
	}
	reportPath, findErr := findJSONReport(directory)
	if findErr != nil {
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
	reportPath, err := resolveReportPath(options.Root, options.ReportPath, options.Authorization)
	if err != nil {
		return Report{}, err
	}
	name, args, _ := commandPlan(options, reportPath)
	previous, err := reportState(reportPath)
	if err != nil {
		return Report{}, err
	}
	result, runErr := service.runner.Run(ctx, options.Root, name, args, output)
	if err := strykerJSRunError(ctx, runErr, result); err != nil {
		return Report{}, err
	}
	if options.Authorization != nil {
		reportPath, err = options.Authorization.Existing(reportPath)
		if err != nil {
			return Report{}, fmt.Errorf("authorize generated StrykerJS report: %w", err)
		}
	}
	data, err := readUpdatedStrykerJSReport(reportPath, previous)
	if err != nil {
		return Report{}, err
	}
	report, err := parseStryker(data, "typescript", "stryker-js", options.MinimumScore)
	if err != nil {
		return Report{}, err
	}
	if result.ExitCode == 1 && !validStrykerJSThresholdExit(result, report) {
		return Report{}, nativeCommandError(npxCommand(), result)
	}
	report.Provenance.NativeExitCode = result.ExitCode
	return report, nil
}

func strykerJSRunError(ctx context.Context, runErr error, result commandResult) error {
	if ctx.Err() != nil {
		return commandError(ctx, runErr)
	}
	if runErr != nil {
		return commandError(ctx, runErr)
	}
	if result.ExitCode != 0 && result.ExitCode != 1 {
		return nativeCommandError(npxCommand(), result)
	}
	return nil
}

func readUpdatedStrykerJSReport(reportPath string, previous fileState) ([]byte, error) {
	current, stateErr := reportState(reportPath)
	if stateErr != nil {
		return nil, stateErr
	}
	if !current.exists || current == previous {
		return nil, fmt.Errorf("StrykerJS did not update JSON report %s", reportPath)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, fmt.Errorf("read StrykerJS report %s: %w", reportPath, err)
	}
	return data, nil
}

type fileState struct {
	exists  bool
	size    int64
	modTime time.Time
	digest  [sha256.Size]byte
}

func resolveReportPath(root, configured string, authorization *rootauth.Root) (string, error) {
	if configured == "" {
		configured = filepath.Join("reports", "mutation", "mutation.json")
	}
	path := configured
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if authorization != nil {
		var err error
		path, err = authorization.Future(path)
		if err != nil {
			return "", fmt.Errorf("authorize StrykerJS report path: %w", err)
		}
	}
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

var (
	strykerJSThresholdLine = regexp.MustCompile(`(?:^|\s)MutationTestReportHelper\s+.*Final mutation score ([0-9]+(?:\.[0-9]+)?) under breaking threshold ([0-9]+(?:\.[0-9]+)?), setting exit code to 1 \(failure\)\.`)
	ansiEscape             = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func validStrykerJSThresholdExit(result commandResult, report Report) bool {
	if result.ExitCode != 1 || report.Score == nil {
		return false
	}
	for _, line := range strings.Split(result.OutputTail, "\n") {
		match := strykerJSThresholdLine.FindStringSubmatch(ansiEscape.ReplaceAllString(line, ""))
		if match == nil {
			continue
		}
		score, scoreErr := strconv.ParseFloat(match[1], 64)
		threshold, thresholdErr := strconv.ParseFloat(match[2], 64)
		return scoreErr == nil && thresholdErr == nil && report.Provenance.NativeBreakThreshold != nil && round(score) == *report.Score && threshold == *report.Provenance.NativeBreakThreshold && score < threshold
	}
	return false
}

func nativeCommandError(name string, result commandResult) error {
	return fmt.Errorf("%s exited with code %d\n%s", name, result.ExitCode, result.OutputTail)
}

func npxCommand() string {
	if runtime.GOOS == "windows" {
		return "npx.cmd"
	}
	return "npx"
}
