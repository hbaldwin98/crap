package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/clioutput"
	"github.com/hbaldwin98/crap/internal/mcpserver"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/hbaldwin98/crap/internal/sarif"
)

type cliOptions struct {
	paths            []string
	format           string
	output           string
	coverage         string
	diffBase         string
	threshold        float64
	failOnThreshold  bool
	includeTests     bool
	includeGenerated bool
	excludes         stringList
	strictCoverage   bool
	showVersion      bool
	showHelp         bool
}

type compareCLIOptions struct {
	paths            []string
	format           string
	output           string
	coverage         string
	base             string
	baseCoverage     string
	threshold        float64
	failOnRegression bool
	includeTests     bool
	includeGenerated bool
	excludes         stringList
	strictCoverage   bool
	showHelp         bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "scope" {
		return runScope(args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "compare" {
		return runComparison(args[1:], stdout, stderr)
	}
	options, ok := parseOptionsWithHelp(args, stdout, stderr)
	if !ok {
		return 1
	}
	if options.showVersion {
		fmt.Fprintln(stdout, buildinfo.CurrentVersion())
		return 0
	}
	if options.showHelp {
		return 0
	}
	return runAnalysis(options, stdout, stderr)
}

func runScope(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "actual" {
		fmt.Fprintln(stderr, "crap: scope requires the actual subcommand")
		return 1
	}
	options, ok := parseOptionsWithHelp(args[1:], stdout, stderr)
	if !ok {
		return 1
	}
	if options.showHelp {
		return 0
	}
	if options.showVersion {
		fmt.Fprintln(stdout, buildinfo.CurrentVersion())
		return 0
	}
	if options.diffBase == "" {
		fmt.Fprintln(stderr, "crap: scope actual requires --diff-base")
		return 1
	}
	if options.format == "sarif" {
		fmt.Fprintln(stderr, "crap: scope actual supports text or json output")
		return 1
	}
	return runScopeAnalysis(options, stdout, stderr)
}

func runComparison(args []string, stdout, stderr io.Writer) int {
	options, ok := parseCompareOptions(args, stdout, stderr)
	if !ok {
		return 1
	}
	if options.showHelp {
		return 0
	}
	if options.base == "" {
		fmt.Fprintln(stderr, "crap: compare requires --base")
		return 1
	}
	if options.output != "" {
		if err := clioutput.Validate(options.output); err != nil {
			fmt.Fprintf(stderr, "crap: invalid output path: %v\n", err)
			return 1
		}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "crap: determine working directory: %v\n", err)
		return 1
	}
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	defer analyzer.Close()
	report, err := analyzer.CompareChangeScope(analysis.ComparisonOptions{
		BaseRevision:     options.base,
		BaseCoveragePath: options.baseCoverage,
		Analysis: analysis.Options{
			Root: root, Paths: options.paths, CoveragePath: options.coverage, CRAPThreshold: options.threshold,
			IncludeTests: options.includeTests, IncludeGenerated: options.includeGenerated, Exclude: options.excludes, StrictCoverage: options.strictCoverage,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		if options.format == "json" {
			encoder := json.NewEncoder(writer)
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		}
		return writeComparisonText(writer, report)
	}); err != nil {
		fmt.Fprintf(stderr, "crap: write report: %v\n", err)
		return 1
	}
	if options.failOnRegression && report.Summary.NewRegressions > 0 {
		return 2
	}
	return 0
}

func parseCompareOptions(args []string, help, stderr io.Writer) (compareCLIOptions, bool) {
	flags := flag.NewFlagSet("crap compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := compareCLIOptions{}
	flags.StringVar(&options.base, "base", "", "Git revision whose merge base supplies baseline source")
	flags.StringVar(&options.baseCoverage, "base-coverage", "", "coverage report generated for baseline source")
	flags.StringVar(&options.coverage, "coverage", "", "coverage report generated for current source")
	flags.StringVar(&options.format, "format", "text", "output format: text or json")
	flags.StringVar(&options.output, "output", "", "write report with safe same-directory replacement")
	flags.Float64Var(&options.threshold, "threshold", 30, "CRAP score threshold")
	flags.BoolVar(&options.failOnRegression, "fail-on-regression", false, "exit 2 for new threshold regressions")
	flags.BoolVar(&options.includeTests, "include-tests", false, "include Go and TypeScript test files")
	flags.BoolVar(&options.includeGenerated, "include-generated", false, "include generated source files")
	flags.Var(&options.excludes, "exclude", "exclude a root-relative path pattern (repeatable)")
	flags.BoolVar(&options.strictCoverage, "strict-coverage", false, "fail when either coverage report has unmatched or ambiguous paths")
	flags.BoolVar(&options.showHelp, "h", false, "print help")
	flags.BoolVar(&options.showHelp, "help", false, "print help")
	writeCompareUsage := func(writer io.Writer) {
		flags.SetOutput(writer)
		fmt.Fprintln(writer, "Usage: crap compare --base REVISION [options] [path ...]")
		flags.PrintDefaults()
		flags.SetOutput(stderr)
	}
	flags.Usage = func() { writeCompareUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return compareCLIOptions{}, false
	}
	options.paths = flags.Args()
	if options.showHelp {
		writeCompareUsage(help)
		return options, true
	}
	if options.format != "text" && options.format != "json" {
		fmt.Fprintf(stderr, "crap: compare does not support format %q\n", options.format)
		return compareCLIOptions{}, false
	}
	if math.IsNaN(options.threshold) || math.IsInf(options.threshold, 0) || options.threshold < 0 {
		fmt.Fprintln(stderr, "crap: threshold must be a finite non-negative number")
		return compareCLIOptions{}, false
	}
	return options, true
}

func writeComparisonText(writer io.Writer, report analysis.ChangeScopeComparisonReport) error {
	if _, err := fmt.Fprintf(writer, "%d new regressions; %d matched, %d added, %d removed, %d ambiguous\n", report.Summary.NewRegressions, report.Summary.Matched, report.Summary.Added, report.Summary.Removed, report.Summary.Ambiguous); err != nil {
		return err
	}
	for _, comparison := range report.Callables {
		if comparison.Status == "matched" && comparison.Change == "unchanged" {
			continue
		}
		if comparison.Status == "matched" {
			baseline, current := comparison.Baseline[0].Method, comparison.Current[0].Method
			if _, err := fmt.Fprintf(writer, "%s %-16s %s (%s:%d) CRAP %.2f -> %.2f\n", comparisonLabel(comparison), comparison.Change, current.Name, filepath.ToSlash(current.File), current.StartLine, baseline.CRAP, current.CRAP); err != nil {
				return err
			}
			continue
		}
		callables := comparison.Current
		if len(callables) == 0 {
			callables = comparison.Baseline
		}
		for _, callable := range callables {
			if _, err := fmt.Fprintf(writer, "%s %-16s %s (%s:%d) CRAP %.2f\n", comparisonLabel(comparison), comparison.Status, callable.Method.Name, filepath.ToSlash(callable.Method.File), callable.Method.StartLine, callable.Method.CRAP); err != nil {
				return err
			}
		}
	}
	if !report.Summary.Complete {
		_, err := fmt.Fprintln(writer, "comparison incomplete: ambiguous callable moves require review")
		return err
	}
	return nil
}

func comparisonLabel(comparison analysis.CallableComparison) string {
	if comparison.NewRegression {
		return "REGRESSION"
	}
	return strings.ToUpper(comparison.Status)
}

func runMCP(args []string, stdout, stderr io.Writer) int {
	options, ok := parseMCPOptionsWithHelp(args, stdout, stderr, "crap")
	if !ok {
		return 1
	}
	if options.showHelp {
		return 0
	}
	policy, err := rootauth.New(options.root, options.allowRoots...)
	if err != nil {
		fmt.Fprintf(stderr, "crap: MCP root policy: %v\n", err)
		return 1
	}
	if err := mcpserver.Run(context.Background(), buildinfo.CurrentVersion(), policy); err != nil {
		fmt.Fprintf(stderr, "crap: MCP server: %v\n", err)
		return 1
	}
	return 0
}

type mcpOptions struct {
	root       string
	allowRoots stringList
	showHelp   bool
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func parseMCPOptions(args []string, stderr io.Writer, name string) (mcpOptions, bool) {
	return parseMCPOptionsWithHelp(args, stderr, stderr, name)
}

func parseMCPOptionsWithHelp(args []string, help, stderr io.Writer, name string) (mcpOptions, bool) {
	flags := flag.NewFlagSet(name+" mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := mcpOptions{}
	flags.StringVar(&options.root, "root", "", "default authorized MCP root")
	flags.Var(&options.allowRoots, "allow-root", "additional authorized MCP root (repeatable)")
	flags.BoolVar(&options.showHelp, "h", false, "print help")
	flags.BoolVar(&options.showHelp, "help", false, "print help")
	writeMCPUsage := func(writer io.Writer) {
		flags.SetOutput(writer)
		fmt.Fprintf(writer, "Usage: %s mcp [--root PATH] [--allow-root PATH ...]\n", name)
		flags.PrintDefaults()
		flags.SetOutput(stderr)
	}
	flags.Usage = func() { writeMCPUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return mcpOptions{}, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: mcp does not accept positional arguments\n", name)
		return mcpOptions{}, false
	}
	if options.showHelp {
		writeMCPUsage(help)
		return options, true
	}
	return options, true
}

func parseOptions(args []string, stderr io.Writer) (cliOptions, bool) {
	return parseOptionsWithHelp(args, stderr, stderr)
}

func parseOptionsWithHelp(args []string, help, stderr io.Writer) (cliOptions, bool) {
	flags := flag.NewFlagSet("crap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := cliOptions{}
	flags.StringVar(&options.format, "format", "text", "output format: text, json, or sarif")
	flags.StringVar(&options.output, "output", "", "write report with safe same-directory replacement")
	flags.StringVar(&options.coverage, "coverage", "", "Cobertura XML or Go coverprofile")
	flags.StringVar(&options.diffBase, "diff-base", "", "only callables touching lines changed from this Git revision")
	flags.Float64Var(&options.threshold, "threshold", 30, "CRAP score threshold")
	flags.BoolVar(&options.failOnThreshold, "fail-on-threshold", false, "exit 2 when a callable exceeds the threshold")
	flags.BoolVar(&options.includeTests, "include-tests", false, "include Go and TypeScript test files")
	flags.BoolVar(&options.includeGenerated, "include-generated", false, "include generated source files")
	flags.Var(&options.excludes, "exclude", "exclude a root-relative path pattern (repeatable)")
	flags.BoolVar(&options.strictCoverage, "strict-coverage", false, "fail when coverage paths are unmatched or ambiguous")
	flags.BoolVar(&options.showVersion, "version", false, "print version")
	flags.BoolVar(&options.showHelp, "h", false, "print help")
	flags.BoolVar(&options.showHelp, "help", false, "print help")
	writeCLIUsage := func(writer io.Writer) {
		flags.SetOutput(writer)
		writeUsage(writer, flags)
		flags.SetOutput(stderr)
	}
	flags.Usage = func() { writeCLIUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return cliOptions{}, false
	}
	options.paths = flags.Args()
	if options.showHelp {
		writeCLIUsage(help)
		return options, true
	}
	if options.showVersion {
		return options, true
	}
	if options.format != "text" && options.format != "json" && options.format != "sarif" {
		fmt.Fprintf(stderr, "crap: unsupported format %q\n", options.format)
		return cliOptions{}, false
	}
	if math.IsNaN(options.threshold) || math.IsInf(options.threshold, 0) || options.threshold < 0 {
		fmt.Fprintln(stderr, "crap: threshold must be a finite non-negative number")
		return cliOptions{}, false
	}
	return options, true
}

func writeUsage(writer io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(writer, "Usage: crap [options] [path ...]")
	fmt.Fprintln(writer, "       crap scope actual --diff-base REVISION [options] [path ...]")
	fmt.Fprintln(writer, "       crap compare --base REVISION [options] [path ...]")
	fmt.Fprintln(writer, "       crap mcp")
	fmt.Fprintln(writer, "Deterministically calculate cyclomatic complexity and CRAP scores for C#, Go, and TypeScript callables.")
	flags.PrintDefaults()
}

func runScopeAnalysis(options cliOptions, stdout, stderr io.Writer) int {
	if options.output != "" {
		if err := clioutput.Validate(options.output); err != nil {
			fmt.Fprintf(stderr, "crap: invalid output path: %v\n", err)
			return 1
		}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "crap: determine working directory: %v\n", err)
		return 1
	}
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	defer analyzer.Close()
	report, err := analyzer.AnalyzeChangeScope(analysis.Options{
		Paths: options.paths, CoveragePath: options.coverage, DiffBase: options.diffBase,
		Root: root, CRAPThreshold: options.threshold, IncludeTests: options.includeTests, IncludeGenerated: options.includeGenerated,
		Exclude: options.excludes, StrictCoverage: options.strictCoverage,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		if options.format == "json" {
			encoder := json.NewEncoder(writer)
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		}
		return writeScopeText(writer, report)
	}); err != nil {
		fmt.Fprintf(stderr, "crap: write report: %v\n", err)
		return 1
	}
	if options.failOnThreshold {
		for _, callable := range report.Callables {
			if callable.AboveThreshold {
				return 2
			}
		}
	}
	return 0
}

func writeScopeText(writer io.Writer, report analysis.ChangeScopeReport) error {
	if _, err := fmt.Fprintf(writer, "%d changed callables in %d changed files\n", report.Summary.ChangedCallables, report.Summary.ChangedFiles); err != nil {
		return err
	}
	for _, file := range report.Files {
		for _, changed := range file.Ranges {
			location := fmt.Sprintf("%s:%d", filepath.ToSlash(file.Path), changed.StartLine)
			if changed.EndLine != changed.StartLine {
				location = fmt.Sprintf("%s-%d", location, changed.EndLine)
			}
			if _, err := fmt.Fprintf(writer, "%s changed\n", location); err != nil {
				return err
			}
		}
	}
	for _, callable := range report.Callables {
		if _, err := fmt.Fprintf(writer, "%s (%s:%d) CRAP %.2f\n", callable.Name, filepath.ToSlash(callable.File), callable.StartLine, callable.CRAP); err != nil {
			return err
		}
	}
	for _, limitation := range report.Limitations {
		if _, err := fmt.Fprintf(writer, "limitation: %s\n", limitation); err != nil {
			return err
		}
	}
	return nil
}

func runAnalysis(options cliOptions, stdout, stderr io.Writer) int {
	if options.output != "" {
		if err := clioutput.Validate(options.output); err != nil {
			fmt.Fprintf(stderr, "crap: invalid output path: %v\n", err)
			return 1
		}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "crap: determine working directory: %v\n", err)
		return 1
	}
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(analysis.Options{
		Paths: options.paths, CoveragePath: options.coverage, DiffBase: options.diffBase,
		Root: root, CRAPThreshold: options.threshold, IncludeTests: options.includeTests, IncludeGenerated: options.includeGenerated,
		Exclude: options.excludes, StrictCoverage: options.strictCoverage,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		return writeReport(writer, report, options.format, root)
	}); err != nil {
		fmt.Fprintf(stderr, "crap: write report: %v\n", err)
		return 1
	}
	if options.failOnThreshold && report.Summary.AboveThreshold > 0 {
		return 2
	}
	return 0
}

func writeReport(writer io.Writer, report analysis.Report, format, root string) error {
	if format == "json" || format == "sarif" {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		if format == "sarif" {
			document, err := analysisSARIF(report, root)
			if err != nil {
				return err
			}
			return encoder.Encode(document)
		}
		return encoder.Encode(report)
	}
	return writeText(writer, report)
}

func writeText(writer io.Writer, report analysis.Report) error {
	methods := append([]analysis.MethodResult(nil), report.Methods...)
	sort.SliceStable(methods, func(i, j int) bool { return methods[i].CRAP > methods[j].CRAP })
	if _, err := fmt.Fprintf(writer, "%-7s %-7s %-9s %s\n", "CRAP", "CC", "COVERAGE", "METHOD"); err != nil {
		return err
	}
	for _, method := range methods {
		coverage := "unknown"
		if method.CoveragePercent != nil {
			coverage = fmt.Sprintf("%.2f%%", *method.CoveragePercent)
		}
		location := fmt.Sprintf("%s:%d", filepath.ToSlash(method.File), method.StartLine)
		if _, err := fmt.Fprintf(writer, "%-7.2f %-7d %-9s %s (%s)\n", method.CRAP, method.Complexity, coverage, method.Name, location); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(writer, "\n%d methods in %d files; %d above CRAP %.2f; maximum %.2f\n",
		report.Summary.Methods, report.Summary.Files, report.Summary.AboveThreshold, report.Threshold, report.Summary.MaximumCRAP); err != nil {
		return err
	}
	for _, exclusion := range report.Discovery.Exclusions {
		if _, err := fmt.Fprintf(writer, "excluded %d discovery entries by %s policy\n", exclusion.Count, exclusion.Reason); err != nil {
			return err
		}
	}
	for _, diagnostic := range report.Diagnostics {
		if _, err := fmt.Fprintf(writer, "%s: %s: %s\n", diagnostic.Severity, diagnostic.Path, diagnostic.Message); err != nil {
			return err
		}
	}
	return nil
}

type analysisSARIFProperties struct {
	Score      float64  `json:"score"`
	Complexity int      `json:"complexity"`
	Coverage   *float64 `json:"coverage"`
	Threshold  float64  `json:"threshold"`
}

func analysisSARIF(report analysis.Report, root string) (sarif.Log, error) {
	methods := append([]analysis.MethodResult(nil), report.Methods...)
	sort.Slice(methods, func(i, j int) bool {
		left, right := methods[i], methods[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.StartLine != right.StartLine {
			return left.StartLine < right.StartLine
		}
		if left.StartColumn != right.StartColumn {
			return left.StartColumn < right.StartColumn
		}
		return left.ID < right.ID
	})
	violationCount := 0
	for _, method := range methods {
		if method.AboveThreshold {
			violationCount++
		}
	}
	if err := sarif.CheckResultLimit(violationCount); err != nil {
		return sarif.Log{}, err
	}
	results := make([]sarif.Result, 0, violationCount)
	sources := make(map[string]*sarif.Source)
	for _, method := range methods {
		if !method.AboveThreshold {
			continue
		}
		source := sources[method.File]
		if source == nil {
			var err error
			source, err = sarif.ReadSource(root, method.File)
			if err != nil {
				return sarif.Log{}, fmt.Errorf("read SARIF source for %s: %w", method.ID, err)
			}
			sources[method.File] = source
		}
		region, err := source.ByteRegion(method.StartLine, method.StartColumn, method.EndLine, method.EndColumn)
		if err != nil {
			return sarif.Log{}, fmt.Errorf("convert SARIF location for %s: %w", method.ID, err)
		}
		results = append(results, sarif.Result{
			RuleID:  "CRAP001",
			Level:   "warning",
			Message: sarif.Message{Text: fmt.Sprintf("%s has CRAP score %.2f, above threshold %.2f", method.Name, method.CRAP, report.Threshold)},
			Locations: []sarif.Location{{PhysicalLocation: sarif.PhysicalLocation{
				ArtifactLocation: sarif.ArtifactLocation{URI: source.URI()},
				Region:           region,
			}}},
			PartialFingerprints: map[string]string{"primaryLocationLineHash": method.ID, "crap.methodId/v1": method.ID},
			Properties: analysisSARIFProperties{
				Score: method.CRAP, Complexity: method.Complexity, Coverage: method.CoveragePercent, Threshold: report.Threshold,
			},
		})
	}
	rules := []sarif.Rule{{
		ID: "CRAP001", Name: "CrapScoreAboveThreshold",
		ShortDescription: sarif.Message{Text: "Callable CRAP score exceeds the configured threshold"},
		FullDescription:  sarif.Message{Text: "A callable's calculated CRAP score is greater than the configured maximum."},
		Help:             sarif.Message{Text: "Reduce complexity, add test coverage, or explicitly adjust the CRAP threshold."},
	}}
	return sarif.New("crap", buildinfo.CurrentVersion(), rules, results), nil
}
