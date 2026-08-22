package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/clioutput"
	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/hbaldwin98/crap/internal/mutationmcp"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/hbaldwin98/crap/internal/sarif"
)

type cliOptions struct {
	language, format, reportPath string
	output                       string
	paths                        []string
	minimumScore                 float64
	timeout                      time.Duration
	workers, testCPU             int
	incremental, fail, version   bool
	dryRun, doctor               bool
	showHelp                     bool
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(args[1:], stdout, stderr)
	}
	doctor := len(args) > 0 && args[0] == "doctor"
	if doctor {
		args = args[1:]
	}
	options, ok := parseOptionsWithHelp(args, stdout, stderr)
	if !ok {
		return 1
	}
	if options.version {
		fmt.Fprintln(stdout, buildinfo.CurrentVersion())
		return 0
	}
	if options.showHelp {
		return 0
	}
	options.doctor = doctor
	if options.format == "sarif" && (options.doctor || options.dryRun) {
		fmt.Fprintln(stderr, "crap-mutate: SARIF is available only for completed mutation reports")
		return 1
	}
	return runMutation(options, stdout, stderr)
}

func runMCP(args []string, stdout, stderr io.Writer) int {
	options, ok := parseMCPOptionsWithHelp(args, stdout, stderr, "crap-mutate")
	if !ok {
		return 1
	}
	if options.showHelp {
		return 0
	}
	policy, err := rootauth.New(options.root, options.allowRoots...)
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: MCP root policy: %v\n", err)
		return 1
	}
	if err := mutationmcp.Run(context.Background(), buildinfo.CurrentVersion(), policy); err != nil {
		fmt.Fprintf(stderr, "crap-mutate: MCP server: %v\n", err)
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

func runMutation(options cliOptions, stdout, stderr io.Writer) int {
	if options.output != "" {
		if err := clioutput.Validate(options.output); err != nil {
			fmt.Fprintf(stderr, "crap-mutate: invalid output path: %v\n", err)
			return 1
		}
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: determine working directory: %v\n", err)
		return 1
	}
	mutationOptions := mutation.Options{
		Root: root, Language: options.language, Paths: options.paths,
		MinimumScore: options.minimumScore, TimeoutSeconds: int(options.timeout.Seconds()),
		Workers: options.workers, TestCPU: options.testCPU,
		Incremental: options.incremental, ReportPath: options.reportPath,
	}
	service := mutation.NewService()
	if options.doctor {
		return runDoctor(service, mutationOptions, options, stdout, stderr)
	}
	if options.dryRun {
		return runPlan(service, mutationOptions, options, stdout, stderr)
	}
	return executeMutation(service, mutationOptions, options, root, stdout, stderr)
}

func runDoctor(service *mutation.Service, mutationOptions mutation.Options, options cliOptions, stdout, stderr io.Writer) int {
	report, err := service.Doctor(context.Background(), mutationOptions)
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: doctor: %v\n", err)
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		return writeDoctor(writer, report, options.format)
	}); err != nil {
		fmt.Fprintf(stderr, "crap-mutate: write doctor report: %v\n", err)
		return 1
	}
	if !report.Ready {
		return 1
	}
	return 0
}

func runPlan(service *mutation.Service, mutationOptions mutation.Options, options cliOptions, stdout, stderr io.Writer) int {
	plan, err := service.Plan(mutationOptions)
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: plan: %v\n", err)
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		return writePlan(writer, plan, options.format)
	}); err != nil {
		fmt.Fprintf(stderr, "crap-mutate: write plan: %v\n", err)
		return 1
	}
	return 0
}

func executeMutation(service *mutation.Service, mutationOptions mutation.Options, options cliOptions, root string, stdout, stderr io.Writer) int {
	report, err := service.Run(context.Background(), mutationOptions, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: %v\n", err)
		return 1
	}
	if err := clioutput.Write(stdout, options.output, func(writer io.Writer) error {
		return writeReport(writer, report, options.format, root)
	}); err != nil {
		fmt.Fprintf(stderr, "crap-mutate: write report: %v\n", err)
		return 1
	}
	return mutationExitCode(options.fail, report.Passed)
}

func mutationExitCode(failOnThreshold, passed bool) int {
	if failOnThreshold && !passed {
		return 2
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (cliOptions, bool) {
	return parseOptionsWithHelp(args, stderr, stderr)
}

func parseOptionsWithHelp(args []string, help, stderr io.Writer) (cliOptions, bool) {
	flags := flag.NewFlagSet("crap-mutate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := cliOptions{}
	flags.StringVar(&options.language, "language", "", "language to mutate: csharp, go, or typescript")
	flags.StringVar(&options.format, "format", "text", "output format: text, json, or sarif")
	flags.StringVar(&options.output, "output", "", "write report with safe same-directory replacement")
	flags.Float64Var(&options.minimumScore, "minimum-score", 80, "minimum accepted mutation score")
	flags.DurationVar(&options.timeout, "timeout", 30*time.Minute, "maximum mutation engine runtime")
	flags.IntVar(&options.workers, "workers", 0, "parallel Gremlins workers; Go only (default 1)")
	flags.IntVar(&options.testCPU, "test-cpu", 0, "CPUs per Gremlins test process; Go only (default 1)")
	flags.BoolVar(&options.incremental, "incremental", false, "enable StrykerJS incremental mode")
	flags.BoolVar(&options.dryRun, "dry-run", false, "print the mutation command plan without running it")
	flags.StringVar(&options.reportPath, "report-path", "", "custom StrykerJS JSON report path")
	flags.BoolVar(&options.fail, "fail-on-threshold", false, "exit 2 when the mutation score is below the minimum")
	flags.BoolVar(&options.version, "version", false, "print version")
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
	if options.version {
		return options, true
	}
	if options.language != "csharp" && options.language != "go" && options.language != "typescript" {
		fmt.Fprintln(stderr, "crap-mutate: --language must be csharp, go, or typescript")
		return cliOptions{}, false
	}
	if options.format != "text" && options.format != "json" && options.format != "sarif" {
		fmt.Fprintf(stderr, "crap-mutate: unsupported format %q\n", options.format)
		return cliOptions{}, false
	}
	if math.IsNaN(options.minimumScore) || math.IsInf(options.minimumScore, 0) || options.minimumScore < 0 || options.minimumScore > 100 {
		fmt.Fprintln(stderr, "crap-mutate: minimum score must be between 0 and 100")
		return cliOptions{}, false
	}
	if options.timeout < time.Second {
		fmt.Fprintln(stderr, "crap-mutate: timeout must be at least one second")
		return cliOptions{}, false
	}
	return options, true
}

func writeUsage(writer io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(writer, "Usage: crap-mutate --language csharp|go|typescript [options] [path ...]")
	fmt.Fprintln(writer, "       crap-mutate doctor --language csharp|go|typescript [options] [path ...]")
	fmt.Fprintln(writer, "       crap-mutate mcp")
	fmt.Fprintln(writer, "Run a language-native mutation engine and normalize its report.")
	flags.PrintDefaults()
}

func writePlan(writer io.Writer, plan mutation.Plan, format string) error {
	if format == "json" {
		return writeJSON(writer, plan)
	}
	_, err := fmt.Fprintf(writer, "root: %s\nengine: %s\ncommand: %s %s\nreport: %s\n",
		plan.Root, plan.Engine, plan.Executable, quotedArguments(plan.Arguments), plan.ReportPath)
	return err
}

func quotedArguments(arguments []string) string {
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		quoted[index] = fmt.Sprintf("%q", argument)
	}
	return strings.Join(quoted, " ")
}

func writeDoctor(writer io.Writer, report mutation.DoctorReport, format string) error {
	if format == "json" {
		return writeJSON(writer, report)
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(writer, "%-8s %-16s %s\n", check.Status, check.Name, check.Message); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(writer, "\nready: %t\n", report.Ready)
	return err
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeReport(writer io.Writer, report mutation.Report, format, root string) error {
	if format == "json" {
		return writeJSON(writer, report)
	}
	if format == "sarif" {
		document, err := mutationSARIF(report, root)
		if err != nil {
			return err
		}
		return writeJSON(writer, document)
	}
	return writeText(writer, report)
}

func writeText(writer io.Writer, report mutation.Report) error {
	findings := append([]mutation.MutantResult(nil), report.Mutants...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Status < findings[j].Status })
	for _, mutant := range findings {
		if mutant.Status != "survived" && mutant.Status != "noCoverage" {
			continue
		}
		if _, err := fmt.Fprintf(writer, "%-12s %s at %s:%d:%d\n", mutant.Status, mutant.Mutator, mutant.File, mutant.Line, mutant.Column); err != nil {
			return err
		}
	}
	score := "unavailable"
	if report.Score != nil {
		score = fmt.Sprintf("%.2f", *report.Score)
	}
	_, err := fmt.Fprintf(writer, "\n%s score %s; minimum %.2f; passed %t; %d mutants (%d survived, %d without coverage)\n",
		report.Engine, score, report.MinimumScore, report.Passed, report.Summary.Total, report.Summary.Survived, report.Summary.NoCoverage)
	return err
}

type mutationSARIFProperties struct {
	Engine       string  `json:"engine"`
	Status       string  `json:"status"`
	MinimumScore float64 `json:"minimumScore"`
}

func mutationSARIF(report mutation.Report, root string) (sarif.Log, error) {
	mutants := append([]mutation.MutantResult(nil), report.Mutants...)
	sort.Slice(mutants, func(i, j int) bool {
		left, right := mutants[i], mutants[j]
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
	actionableCount := 0
	for _, mutant := range mutants {
		if mutant.Status == "survived" || mutant.Status == "noCoverage" {
			actionableCount++
		}
	}
	if err := sarif.CheckResultLimit(actionableCount); err != nil {
		return sarif.Log{}, err
	}
	results := make([]sarif.Result, 0, actionableCount)
	sources := make(map[string]*sarif.Source)
	for _, mutant := range mutants {
		ruleID := ""
		switch mutant.Status {
		case "survived":
			ruleID = "MUT001"
		case "noCoverage":
			ruleID = "MUT002"
		default:
			continue
		}
		source := sources[mutant.File]
		if source == nil {
			var err error
			source, err = sarif.ReadSource(root, mutant.File)
			if err != nil {
				return sarif.Log{}, fmt.Errorf("read SARIF source for %s: %w", mutant.ID, err)
			}
			sources[mutant.File] = source
		}
		var region sarif.Region
		var err error
		switch report.Engine {
		case "gremlins":
			region, err = source.BytePointRegion(mutant.StartLine, mutant.StartColumn)
		case "stryker-js", "stryker-net":
			if mutant.EndLine == nil || mutant.EndColumn == nil {
				return sarif.Log{}, fmt.Errorf("convert SARIF location for %s: Stryker range has no end", mutant.ID)
			}
			if mutant.StartLine == *mutant.EndLine && mutant.StartColumn == *mutant.EndColumn {
				region, err = source.UTF16PointRegion(mutant.StartLine, mutant.StartColumn)
			} else {
				region, err = source.UTF16Region(mutant.StartLine, mutant.StartColumn, *mutant.EndLine, *mutant.EndColumn)
			}
		default:
			return sarif.Log{}, fmt.Errorf("convert SARIF location for %s: unsupported engine %q", mutant.ID, report.Engine)
		}
		if err != nil {
			return sarif.Log{}, fmt.Errorf("convert SARIF location for %s: %w", mutant.ID, err)
		}
		results = append(results, sarif.Result{
			RuleID:  ruleID,
			Level:   "warning",
			Message: sarif.Message{Text: fmt.Sprintf("%s mutant %s (%s)", mutant.Status, mutant.Mutator, mutant.ID)},
			Locations: []sarif.Location{{PhysicalLocation: sarif.PhysicalLocation{
				ArtifactLocation: sarif.ArtifactLocation{URI: source.URI()},
				Region:           region,
			}}},
			PartialFingerprints: map[string]string{"primaryLocationLineHash": mutant.ID, "crap-mutate.mutantId/v1": mutant.ID},
			Properties: mutationSARIFProperties{
				Engine: report.Engine, Status: mutant.Status, MinimumScore: report.MinimumScore,
			},
		})
	}
	rules := []sarif.Rule{
		{
			ID: "MUT001", Name: "SurvivedMutant", ShortDescription: sarif.Message{Text: "Mutation survived the test suite"},
			FullDescription: sarif.Message{Text: "A source mutation was exercised but not detected by the test suite."},
			Help:            sarif.Message{Text: "Add or strengthen tests that fail when this source behavior is mutated."},
		},
		{
			ID: "MUT002", Name: "UncoveredMutant", ShortDescription: sarif.Message{Text: "Mutation was not covered by tests"},
			FullDescription: sarif.Message{Text: "A source mutation was not exercised by the test suite."},
			Help:            sarif.Message{Text: "Add tests that execute this source location and assert its behavior."},
		},
	}
	return sarif.New("crap-mutate", buildinfo.CurrentVersion(), rules, results), nil
}
