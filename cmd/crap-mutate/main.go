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
	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/hbaldwin98/crap/internal/mutationmcp"
	"github.com/hbaldwin98/crap/internal/rootauth"
)

type cliOptions struct {
	language, format, reportPath string
	paths                        []string
	minimumScore                 float64
	timeout                      time.Duration
	incremental, fail, version   bool
	dryRun, doctor               bool
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(args[1:], stderr)
	}
	doctor := len(args) > 0 && args[0] == "doctor"
	if doctor {
		args = args[1:]
	}
	options, ok := parseOptions(args, stderr)
	if !ok {
		return 1
	}
	if options.version {
		fmt.Fprintln(stdout, buildinfo.CurrentVersion())
		return 0
	}
	options.doctor = doctor
	return runMutation(options, stdout, stderr)
}

func runMCP(args []string, stderr io.Writer) int {
	options, ok := parseMCPOptions(args, stderr, "crap-mutate")
	if !ok {
		return 1
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
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func parseMCPOptions(args []string, stderr io.Writer, name string) (mcpOptions, bool) {
	flags := flag.NewFlagSet(name+" mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := mcpOptions{}
	flags.StringVar(&options.root, "root", "", "default authorized MCP root")
	flags.Var(&options.allowRoots, "allow-root", "additional authorized MCP root (repeatable)")
	if err := flags.Parse(args); err != nil {
		return mcpOptions{}, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "%s: mcp does not accept positional arguments\n", name)
		return mcpOptions{}, false
	}
	return options, true
}

func runMutation(options cliOptions, stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: determine working directory: %v\n", err)
		return 1
	}
	mutationOptions := mutation.Options{
		Root: root, Language: options.language, Paths: options.paths,
		MinimumScore: options.minimumScore, TimeoutSeconds: int(options.timeout.Seconds()),
		Incremental: options.incremental, ReportPath: options.reportPath,
	}
	service := mutation.NewService()
	if options.doctor {
		report, err := service.Doctor(context.Background(), mutationOptions)
		if err != nil {
			fmt.Fprintf(stderr, "crap-mutate: doctor: %v\n", err)
			return 1
		}
		if err := writeDoctor(stdout, report, options.format); err != nil {
			fmt.Fprintf(stderr, "crap-mutate: write doctor report: %v\n", err)
			return 1
		}
		if !report.Ready {
			return 1
		}
		return 0
	}
	if options.dryRun {
		plan, err := service.Plan(mutationOptions)
		if err != nil {
			fmt.Fprintf(stderr, "crap-mutate: plan: %v\n", err)
			return 1
		}
		if err := writePlan(stdout, plan, options.format); err != nil {
			fmt.Fprintf(stderr, "crap-mutate: write plan: %v\n", err)
			return 1
		}
		return 0
	}
	report, err := service.Run(context.Background(), mutationOptions, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "crap-mutate: %v\n", err)
		return 1
	}
	if err := writeReport(stdout, report, options.format); err != nil {
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
	flags := flag.NewFlagSet("crap-mutate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := cliOptions{}
	flags.StringVar(&options.language, "language", "", "language to mutate: csharp, go, or typescript")
	flags.StringVar(&options.format, "format", "text", "output format: text or json")
	flags.Float64Var(&options.minimumScore, "minimum-score", 80, "minimum accepted mutation score")
	flags.DurationVar(&options.timeout, "timeout", 30*time.Minute, "maximum mutation engine runtime")
	flags.BoolVar(&options.incremental, "incremental", false, "enable StrykerJS incremental mode")
	flags.BoolVar(&options.dryRun, "dry-run", false, "print the mutation command plan without running it")
	flags.StringVar(&options.reportPath, "report-path", "", "custom StrykerJS JSON report path")
	flags.BoolVar(&options.fail, "fail-on-threshold", false, "exit 2 when the mutation score is below the minimum")
	flags.BoolVar(&options.version, "version", false, "print version")
	flags.Usage = func() { writeUsage(stderr, flags) }
	if err := flags.Parse(args); err != nil {
		return cliOptions{}, false
	}
	options.paths = flags.Args()
	if options.version {
		return options, true
	}
	if options.language != "csharp" && options.language != "go" && options.language != "typescript" {
		fmt.Fprintln(stderr, "crap-mutate: --language must be csharp, go, or typescript")
		return cliOptions{}, false
	}
	if options.format != "text" && options.format != "json" {
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
	fmt.Fprintf(writer, "root: %s\nengine: %s\ncommand: %s %s\nreport: %s\n",
		plan.Root, plan.Engine, plan.Executable, quotedArguments(plan.Arguments), plan.ReportPath)
	return nil
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
		fmt.Fprintf(writer, "%-8s %-16s %s\n", check.Status, check.Name, check.Message)
	}
	fmt.Fprintf(writer, "\nready: %t\n", report.Ready)
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeReport(writer io.Writer, report mutation.Report, format string) error {
	if format == "json" {
		return writeJSON(writer, report)
	}
	writeText(writer, report)
	return nil
}

func writeText(writer io.Writer, report mutation.Report) {
	findings := append([]mutation.MutantResult(nil), report.Mutants...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Status < findings[j].Status })
	for _, mutant := range findings {
		if mutant.Status != "survived" && mutant.Status != "noCoverage" {
			continue
		}
		fmt.Fprintf(writer, "%-12s %s at %s:%d:%d\n", mutant.Status, mutant.Mutator, mutant.File, mutant.Line, mutant.Column)
	}
	score := "unavailable"
	if report.Score != nil {
		score = fmt.Sprintf("%.2f", *report.Score)
	}
	fmt.Fprintf(writer, "\n%s score %s; minimum %.2f; passed %t; %d mutants (%d survived, %d without coverage)\n",
		report.Engine, score, report.MinimumScore, report.Passed, report.Summary.Total, report.Summary.Survived, report.Summary.NoCoverage)
}
