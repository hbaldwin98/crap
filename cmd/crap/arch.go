package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/clioutput"
)

// archCLIOptions holds the `crap arch` command-line options.
type archCLIOptions struct {
	paths            []string
	format           string
	output           string
	rulesFile        string
	coverage         string
	threshold        float64
	includeTests     bool
	includeGenerated bool
	excludes         stringList
	strictCoverage   bool
	showHelp         bool
}

func runArchitecture(args []string, stdout, stderr io.Writer) int {
	options, ok := parseArchitectureOptions(args, stdout, stderr)
	if !ok {
		return 1
	}
	if options.showHelp {
		return 0
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
	graph, err := analyzer.AnalyzeCodeGraph(analysis.CodeGraphOptions{
		Root: root, Paths: options.paths, CoveragePath: options.coverage, CRAPThreshold: options.threshold,
		IncludeTests: options.includeTests, IncludeGenerated: options.includeGenerated, Exclude: options.excludes, StrictCoverage: options.strictCoverage,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return 1
	}

	rules := analysis.ArchitectureRules{}
	if options.rulesFile != "" {
		parsed, ok := parseArchitectureRules(options.rulesFile, stderr)
		if !ok {
			return 1
		}
		rules = parsed
	}

	report, err := analysis.AnalyzeArchitecture(context.Background(), graph, rules)
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
		writeArchitectureText(writer, report)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "crap: write report: %v\n", err)
		return 1
	}
	if !report.Summary.Complete {
		return 2
	}
	return 0
}

func parseArchitectureOptions(args []string, help, stderr io.Writer) (archCLIOptions, bool) {
	flags := flag.NewFlagSet("crap arch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := archCLIOptions{}
	flags.StringVar(&options.format, "format", "text", "output format: text or json")
	flags.StringVar(&options.output, "output", "", "write report with safe same-directory replacement")
	flags.StringVar(&options.rulesFile, "rules", "", "path to a JSON file with architecture rules")
	flags.StringVar(&options.coverage, "coverage", "", "optional Cobertura XML or Go coverprofile")
	flags.Float64Var(&options.threshold, "threshold", 30, "CRAP score threshold for callable decoration")
	flags.BoolVar(&options.includeTests, "include-tests", false, "include Go and TypeScript test files")
	flags.BoolVar(&options.includeGenerated, "include-generated", false, "include generated source files")
	flags.Var(&options.excludes, "exclude", "exclude a root-relative path pattern (repeatable)")
	flags.BoolVar(&options.strictCoverage, "strict-coverage", false, "fail when coverage paths are unmatched or ambiguous")
	flags.BoolVar(&options.showHelp, "h", false, "print help")
	flags.BoolVar(&options.showHelp, "help", false, "print help")
	writeUsage := func(writer io.Writer) {
		flags.SetOutput(writer)
		fmt.Fprintln(writer, "Usage: crap arch [options] [path ...]")
		fmt.Fprintln(writer, "Evaluate architecture rules against the module dependency graph.")
		flags.PrintDefaults()
		flags.SetOutput(stderr)
	}
	flags.Usage = func() { writeUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return archCLIOptions{}, false
	}
	options.paths = flags.Args()
	if options.showHelp {
		writeUsage(help)
		return options, true
	}
	if options.format != "text" && options.format != "json" {
		fmt.Fprintf(stderr, "crap: arch does not support format %q\n", options.format)
		return archCLIOptions{}, false
	}
	return options, true
}

func parseArchitectureRules(path string, stderr io.Writer) (analysis.ArchitectureRules, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "crap: read rules: %v\n", err)
		return analysis.ArchitectureRules{}, false
	}
	var rules analysis.ArchitectureRules
	if err := json.Unmarshal(data, &rules); err != nil {
		fmt.Fprintf(stderr, "crap: parse rules: %v\n", err)
		return analysis.ArchitectureRules{}, false
	}
	return rules, true
}

func writeArchitectureText(writer io.Writer, report analysis.ArchitectureReport) {
	fmt.Fprintf(writer, "%d modules, %d dependencies, %d cycles, %d violations\n", report.Summary.Modules, report.Summary.Edges, report.Summary.Cycles, report.Summary.Violations)
	for _, cycle := range report.Cycles {
		first := ""
		if len(cycle.Edges) > 0 {
			first = fmt.Sprintf("%s -> %s", cycle.Edges[0].From, cycle.Edges[0].To)
		}
		fmt.Fprintf(writer, "CYCLE %s\n", first)
		for _, edge := range cycle.Edges {
			fmt.Fprintf(writer, "  %s -> %s\n", edge.From, edge.To)
		}
	}
	for _, violation := range report.Violations {
		reason := violation.Reason
		if violation.Reason == "" && violation.Kind == "cycle" {
			reason = "cycle"
		}
		fmt.Fprintf(writer, "VIOLATION %s %s -> %s%s\n", violation.Kind, violation.From, violation.To, optionalReason(reason))
	}
	for _, limitation := range report.Limitations {
		fmt.Fprintf(writer, "LIMITATION %s\n", limitation)
	}
}

func optionalReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}
