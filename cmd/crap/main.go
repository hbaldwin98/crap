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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "mcp" {
		if err := mcpserver.Run(context.Background(), version); err != nil {
			fmt.Fprintf(stderr, "crap: MCP server: %v\n", err)
			return 1
		}
		return 0
	}
	flags := flag.NewFlagSet("crap", flag.ContinueOnError)
	flags.SetOutput(stderr)
	format := flags.String("format", "text", "output format: text or json")
	coverage := flags.String("coverage", "", "Cobertura XML or Go coverprofile")
	diffBase := flags.String("diff-base", "", "only callables touching lines changed from this Git revision")
	threshold := flags.Float64("threshold", 30, "CRAP score threshold")
	failOnThreshold := flags.Bool("fail-on-threshold", false, "exit 2 when a callable exceeds the threshold")
	showVersion := flags.Bool("version", false, "print version")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: crap [options] [path ...]")
		fmt.Fprintln(stderr, "       crap mcp")
		fmt.Fprintln(stderr, "Deterministically calculate cyclomatic complexity and CRAP scores for C# and Go callables.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *showVersion {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(stderr, "crap: unsupported format %q\n", *format)
		return 1
	}
	if *threshold < 0 {
		fmt.Fprintln(stderr, "crap: threshold must not be negative")
		return 1
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
		Paths:         flags.Args(),
		CoveragePath:  *coverage,
		DiffBase:      *diffBase,
		Root:          root,
		CRAPThreshold: *threshold,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}
	if *format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Fprintf(stderr, "crap: write report: %v\n", err)
			return 1
		}
	} else {
		writeText(stdout, report)
	}
	if *failOnThreshold && report.Summary.AboveThreshold > 0 {
		return 2
	}
	return 0
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
