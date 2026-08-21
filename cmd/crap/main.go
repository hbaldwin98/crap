package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/mcpserver"
)

const version = "0.1.0"

type cliOptions struct {
	paths           []string
	format          string
	coverage        string
	diffBase        string
	threshold       float64
	failOnThreshold bool
	includeTests    bool
	showVersion     bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(stderr)
	}
	options, ok := parseOptions(args, stderr)
	if !ok {
		return 1
	}
	if options.showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	return runAnalysis(options, stdout, stderr)
}

func runMCP(stderr io.Writer) int {
	if err := mcpserver.Run(context.Background(), version); err != nil {
		fmt.Fprintf(stderr, "crap: MCP server: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (cliOptions, bool) {
	flags := flag.NewFlagSet("crap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := cliOptions{}
	flags.StringVar(&options.format, "format", "text", "output format: text or json")
	flags.StringVar(&options.coverage, "coverage", "", "Cobertura XML or Go coverprofile")
	flags.StringVar(&options.diffBase, "diff-base", "", "only callables touching lines changed from this Git revision")
	flags.Float64Var(&options.threshold, "threshold", 30, "CRAP score threshold")
	flags.BoolVar(&options.failOnThreshold, "fail-on-threshold", false, "exit 2 when a callable exceeds the threshold")
	flags.BoolVar(&options.includeTests, "include-tests", false, "include Go and TypeScript test files")
	flags.BoolVar(&options.showVersion, "version", false, "print version")
	flags.Usage = func() { writeUsage(stderr, flags) }
	if err := flags.Parse(args); err != nil {
		return cliOptions{}, false
	}
	options.paths = flags.Args()
	if options.format != "text" && options.format != "json" {
		fmt.Fprintf(stderr, "crap: unsupported format %q\n", options.format)
		return cliOptions{}, false
	}
	if options.threshold < 0 {
		fmt.Fprintln(stderr, "crap: threshold must not be negative")
		return cliOptions{}, false
	}
	return options, true
}

func writeUsage(writer io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(writer, "Usage: crap [options] [path ...]")
	fmt.Fprintln(writer, "       crap mcp")
	fmt.Fprintln(writer, "Deterministically calculate cyclomatic complexity and CRAP scores for C#, Go, and TypeScript callables.")
	flags.PrintDefaults()
}

func runAnalysis(options cliOptions, stdout, stderr io.Writer) int {
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
		Root: root, CRAPThreshold: options.threshold, IncludeTests: options.includeTests,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	if err := writeReport(stdout, report, options.format); err != nil {
		fmt.Fprintf(stderr, "crap: write report: %v\n", err)
		return 1
	}
	if options.failOnThreshold && report.Summary.AboveThreshold > 0 {
		return 2
	}
	return 0
}

func writeReport(writer io.Writer, report analysis.Report, format string) error {
	if format == "json" {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	writeText(writer, report)
	return nil
}

func writeText(writer io.Writer, report analysis.Report) {
	methods := append([]analysis.MethodResult(nil), report.Methods...)
	sort.SliceStable(methods, func(i, j int) bool { return methods[i].CRAP > methods[j].CRAP })
	fmt.Fprintf(writer, "%-7s %-7s %-9s %s\n", "CRAP", "CC", "COVERAGE", "METHOD")
	for _, method := range methods {
		coverage := "unknown"
		if method.CoveragePercent != nil {
			coverage = fmt.Sprintf("%.2f%%", *method.CoveragePercent)
		}
		location := fmt.Sprintf("%s:%d", filepath.ToSlash(method.File), method.StartLine)
		fmt.Fprintf(writer, "%-7.2f %-7d %-9s %s (%s)\n", method.CRAP, method.Complexity, coverage, method.Name, location)
	}
	fmt.Fprintf(writer, "\n%d methods in %d files; %d above CRAP %.2f; maximum %.2f\n",
		report.Summary.Methods, report.Summary.Files, report.Summary.AboveThreshold, report.Threshold, report.Summary.MaximumCRAP)
}
