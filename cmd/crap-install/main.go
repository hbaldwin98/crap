package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/hbaldwin98/crap/internal/installer"
)

type clientFlags []string

var moduleVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)

func (values *clientFlags) String() string { return strings.Join(*values, ",") }
func (values *clientFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type options struct {
	version string
	clients clientFlags
	dryRun  bool
	help    bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	options, ok := parseOptions(args, stdout, stderr)
	if !ok {
		return 2
	}
	if options.help {
		return 0
	}
	system := installer.System{
		Runner: installer.ExecRunner{Stdout: stdout, Stderr: stderr}, LookPath: exec.LookPath,
		Getenv: os.Getenv, HomeDir: os.UserHomeDir, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}
	err := installer.Run(system, installer.Options{
		Version: options.version, Clients: options.clients, DryRun: options.dryRun, Stdout: stdout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "crap-install: %v\n", err)
		return 1
	}
	return 0
}

func parseOptions(args []string, help, stderr io.Writer) (options, bool) {
	flags := flag.NewFlagSet("crap-install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options options
	flags.StringVar(&options.version, "version", "latest", "module version for both binaries")
	flags.Var(&options.clients, "client", "client(s): claude, opencode, generic; repeatable or comma-separated")
	flags.BoolVar(&options.dryRun, "dry-run", false, "print actions without installing or writing config")
	flags.BoolVar(&options.help, "help", false, "print help")
	flags.BoolVar(&options.help, "h", false, "print help")
	flags.Usage = func() { writeUsage(flags.Output(), flags) }
	if err := flags.Parse(args); err != nil {
		return options, false
	}
	if options.help {
		flags.SetOutput(help)
		writeUsage(help, flags)
		return options, true
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "crap-install: positional arguments are not supported")
		return options, false
	}
	if !validVersion(options.version) {
		fmt.Fprintf(stderr, "crap-install: invalid module version %q\n", options.version)
		return options, false
	}
	if _, err := installer.NormalizeClients(options.clients); err != nil {
		fmt.Fprintf(stderr, "crap-install: %v\n", err)
		return options, false
	}
	return options, true
}

func validVersion(version string) bool {
	if version == "latest" {
		return true
	}
	if moduleVersionPattern.MatchString(version) {
		return true
	}
	if len(version) < 7 || len(version) > 40 {
		return false
	}
	for _, char := range version {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func writeUsage(writer io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(writer, "Usage: crap-install [--client NAME[,NAME...]] [--version VERSION] [--dry-run]")
	fmt.Fprintln(writer, "Install crap and crap-mutate, configure detected MCP clients, and write a generic MCP config.")
	flags.PrintDefaults()
}
