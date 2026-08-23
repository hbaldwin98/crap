package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/clioutput"
)

type callsCLIOptions struct {
	paths            []string
	format           string
	output           string
	diffBase         string
	includeGenerated bool
	excludes         stringList
	showHelp         bool
}

func runCalls(args []string, stdout, stderr io.Writer) int {
	options, ok := parseCallsOptions(args, stdout, stderr)
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
	report, ok := analyzeCallGraph(options, stderr)
	if !ok {
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		if options.format == "json" {
			encoder := json.NewEncoder(writer)
			encoder.SetIndent("", "  ")
			return encoder.Encode(report)
		}
		return writeCallsText(writer, report)
	}); err != nil {
		fmt.Fprintf(stderr, "crap: write report: %v\n", err)
		return 1
	}
	return 0
}

func analyzeCallGraph(options callsCLIOptions, stderr io.Writer) (analysis.CallGraphReport, bool) {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "crap: determine working directory: %v\n", err)
		return analysis.CallGraphReport{}, false
	}
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return analysis.CallGraphReport{}, false
	}
	defer analyzer.Close()
	report, err := analyzer.AnalyzeCallGraph(analysis.CallGraphOptions{
		Root: root, Paths: options.paths, DiffBase: options.diffBase,
		IncludeGenerated: options.includeGenerated, Exclude: options.excludes,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap: %v\n", err)
		return analysis.CallGraphReport{}, false
	}
	return report, true
}

func parseCallsOptions(args []string, help, stderr io.Writer) (callsCLIOptions, bool) {
	flags := flag.NewFlagSet("crap calls", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := callsCLIOptions{}
	flags.StringVar(&options.format, "format", "text", "output format: text or json")
	flags.StringVar(&options.output, "output", "", "write report with safe same-directory replacement")
	flags.StringVar(&options.diffBase, "diff-base", "", "git revision whose merge base marks unchanged callables")
	flags.BoolVar(&options.includeGenerated, "include-generated", false, "include generated source files")
	flags.Var(&options.excludes, "exclude", "exclude a root-relative path pattern (repeatable)")
	flags.BoolVar(&options.showHelp, "h", false, "print help")
	flags.BoolVar(&options.showHelp, "help", false, "print help")
	writeUsage := func(writer io.Writer) {
		flags.SetOutput(writer)
		fmt.Fprintln(writer, "Usage: crap calls [options] [path ...]")
		fmt.Fprintln(writer, "Build a compiler-backed call graph and, with --diff-base, affected tests.")
		flags.PrintDefaults()
		flags.SetOutput(stderr)
	}
	flags.Usage = func() { writeUsage(stderr) }
	if err := flags.Parse(args); err != nil {
		return callsCLIOptions{}, false
	}
	options.paths = flags.Args()
	if options.showHelp {
		writeUsage(help)
		return options, true
	}
	if options.format != "text" && options.format != "json" {
		fmt.Fprintf(stderr, "crap: calls does not support format %q\n", options.format)
		return callsCLIOptions{}, false
	}
	return options, true
}

func writeCallsText(writer io.Writer, report analysis.CallGraphReport) error {
	if _, err := fmt.Fprintf(writer, "%d functions, %d edges (%d static, %d dispatch) across %d packages\n", report.Summary.Functions, report.Summary.Edges, report.Summary.StaticEdges, report.Summary.DispatchEdges, report.Summary.Packages); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "%d tests, %d changed callables, %d affected tests, %d unresolved calls\n", report.Summary.Tests, report.Summary.ChangedCallables, report.Summary.AffectedTests, report.Summary.UnresolvedCalls); err != nil {
		return err
	}
	if err := writeCallsTextAffectedTests(writer, report.AffectedTests); err != nil {
		return err
	}
	for _, limitation := range report.Limitations {
		if _, err := fmt.Fprintf(writer, "limitation: %s\n", limitation); err != nil {
			return err
		}
	}
	return nil
}

func writeCallsTextAffectedTests(writer io.Writer, tests []analysis.CallGraphAffectedTest) error {
	for _, test := range tests {
		if _, err := fmt.Fprintf(writer, "AFFECTED %s %s distance %d seeds %s\n", test.Name, filepath.ToSlash(test.File), test.Distance, strings.Join(test.Seeds, ",")); err != nil {
			return err
		}
	}
	return nil
}
