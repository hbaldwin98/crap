package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hbaldwin98/crap/internal/clioutput"
)

const module = "github.com/hbaldwin98/crap"

var serverNames = []string{"crap", "crap-mutate"}

type Runner interface {
	Run(name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

type System struct {
	Runner   Runner
	LookPath func(string) (string, error)
	Getenv   func(string) string
	HomeDir  func() (string, error)
	GOOS     string
	GOARCH   string
}

type Options struct {
	Version string
	Clients []string
	DryRun  bool
	Stdout  io.Writer
}

type preparedFile struct {
	path     string
	contents []byte
	existed  bool
	original []byte
}

type plan struct {
	options  Options
	paths    map[string]string
	generic  preparedFile
	openCode []preparedFile
	claude   string
}

func Run(system System, options Options) error {
	prepared, err := prepare(system, options)
	if err != nil {
		return err
	}
	return execute(system, prepared)
}

func prepare(system System, options Options) (plan, error) {
	if err := validateGoTarget(system); err != nil {
		return plan{}, err
	}
	home, err := system.HomeDir()
	if err != nil {
		return plan{}, fmt.Errorf("determine home directory: %w", err)
	}
	binDir, err := binaryDirectory(system)
	if err != nil {
		return plan{}, err
	}
	prepared := plan{options: options, paths: binaryPaths(binDir, system.GOOS)}
	clients, err := selectedClients(system, options.Clients)
	if err != nil {
		return plan{}, err
	}
	prepared.generic, err = prepareGeneric(filepath.Join(home, ".config", "crap", "mcp.json"), prepared.paths)
	if err != nil {
		return plan{}, err
	}
	for _, client := range clients {
		switch client {
		case "claude":
			prepared.claude, err = system.LookPath("claude")
		case "opencode":
			prepared.openCode, err = prepareOpenCode(system, home, prepared.paths)
		}
		if err != nil {
			return plan{}, fmt.Errorf("prepare %s configuration: %w", client, err)
		}
	}
	return prepared, nil
}

func execute(system System, prepared plan) error {
	installArgs := []string{"install", module + "/cmd/crap@" + prepared.options.Version, module + "/cmd/crap-mutate@" + prepared.options.Version}
	if prepared.options.DryRun {
		printDryRun(prepared, installArgs)
		return nil
	}
	if err := system.Runner.Run("go", installArgs...); err != nil {
		return fmt.Errorf("install binaries: %w", err)
	}
	if err := verifyBinaries(prepared.paths); err != nil {
		return err
	}
	fmt.Fprintf(prepared.options.Stdout, "Installed binaries: %s, %s\n", prepared.paths["crap"], prepared.paths["crap-mutate"])
	files := append([]preparedFile{prepared.generic}, prepared.openCode...)
	if err := verifyPreparedFiles(files); err != nil {
		return err
	}
	if err := commitFile(prepared.generic); err != nil {
		return err
	}
	fmt.Fprintf(prepared.options.Stdout, "Wrote generic MCP config: %s\n", prepared.generic.path)
	for _, file := range prepared.openCode {
		if err := commitFile(file); err != nil {
			return err
		}
		fmt.Fprintf(prepared.options.Stdout, "Updated OpenCode config: %s\n", file.path)
	}
	if prepared.claude != "" {
		if err := configureClaude(system.Runner, prepared.claude, prepared.paths, prepared.options.Stdout); err != nil {
			return err
		}
	}
	return nil
}

func verifyPreparedFiles(files []preparedFile) error {
	for _, file := range files {
		if err := verifyPreparedDestination(file); err != nil {
			return err
		}
	}
	return nil
}

func printDryRun(prepared plan, installArgs []string) {
	stdout := prepared.options.Stdout
	fmt.Fprintf(stdout, "Would run: %s\n", formatArgv(append([]string{"go"}, installArgs...)))
	fmt.Fprintf(stdout, "Would write generic MCP config: %s\n", prepared.generic.path)
	for _, file := range prepared.openCode {
		fmt.Fprintf(stdout, "Would update OpenCode config: %s\n", file.path)
	}
	if prepared.claude != "" {
		for _, name := range serverNames {
			argv := append([]string{prepared.claude}, claudeAddArgs(name, prepared.paths[name])...)
			fmt.Fprintf(stdout, "Would run: %s\n", formatArgv(argv))
		}
	}
}

func formatArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = strconv.Quote(arg)
	}
	return strings.Join(quoted, " ")
}

func validateGoTarget(system System) error {
	for _, target := range []struct {
		name string
		host string
	}{{"GOOS", system.GOOS}, {"GOARCH", system.GOARCH}} {
		actual, err := goEnv(system.Runner, target.name)
		if err != nil {
			return err
		}
		if actual != target.host {
			return fmt.Errorf("go env %s is %q; installer host is %q (cross-compilation is not supported)", target.name, actual, target.host)
		}
	}
	return nil
}

func binaryDirectory(system System) (string, error) {
	gobin, err := goEnv(system.Runner, "GOBIN")
	if err != nil {
		return "", err
	}
	if gobin != "" {
		return filepath.Abs(gobin)
	}
	gopath, err := goEnv(system.Runner, "GOPATH")
	if err != nil {
		return "", err
	}
	first := firstGoPath(gopath, system.GOOS)
	if first == "" {
		return "", errors.New("go env GOPATH returned no paths")
	}
	return filepath.Abs(filepath.Join(first, "bin"))
}

func firstGoPath(gopath, goos string) string {
	separator := ":"
	if goos == "windows" {
		separator = ";"
	}
	return strings.Split(gopath, separator)[0]
}

func goEnv(runner Runner, key string) (string, error) {
	output, err := runner.Output("go", "env", key)
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, commandError(output, err))
	}
	return strings.TrimSpace(string(output)), nil
}

func binaryPaths(directory, goos string) map[string]string {
	extension := ""
	if goos == "windows" {
		extension = ".exe"
	}
	return map[string]string{
		"crap":        filepath.Join(directory, "crap"+extension),
		"crap-mutate": filepath.Join(directory, "crap-mutate"+extension),
	}
}

func verifyBinaries(paths map[string]string) error {
	for _, name := range serverNames {
		info, err := os.Stat(paths[name])
		if err != nil {
			return fmt.Errorf("verify installed %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("verify installed %s: not a regular file", name)
		}
	}
	return nil
}

func selectedClients(system System, requested []string) ([]string, error) {
	if len(requested) > 0 {
		return NormalizeClients(requested)
	}
	var clients []string
	for _, name := range []string{"claude", "opencode"} {
		if _, err := system.LookPath(name); err == nil {
			clients = append(clients, name)
		}
	}
	return clients, nil
}

func NormalizeClients(values []string) ([]string, error) {
	seen := make(map[string]bool)
	var clients []string
	for _, value := range values {
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "claude" && name != "opencode" && name != "generic" {
				return nil, fmt.Errorf("unsupported client %q", name)
			}
			if name != "generic" && !seen[name] {
				seen[name] = true
				clients = append(clients, name)
			}
		}
	}
	return clients, nil
}

func validateDestination(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination must not be a symlink")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("destination is not a regular file")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("access destination: %w", err)
	}
	parent := filepath.Dir(path)
	for {
		info, err = os.Stat(parent)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("config parent is not a directory")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("access config parent: %w", err)
		}
		next := filepath.Dir(parent)
		if next == parent {
			return fmt.Errorf("no existing config parent directory")
		}
		parent = next
	}
}

func commitFile(file preparedFile) error {
	if err := verifyPreparedDestination(file); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := clioutput.WriteChecked(io.Discard, file.path, func(writer io.Writer) error {
		_, err := writer.Write(file.contents)
		return err
	}, func() error {
		return verifyPreparedDestination(file)
	}); err != nil {
		return fmt.Errorf("write %s: %w", file.path, err)
	}
	return nil
}

func verifyPreparedDestination(file preparedFile) error {
	info, err := os.Lstat(file.path)
	if !file.existed {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return configChangedError(file.path, "destination cannot be accessed")
		}
		return configChangedError(file.path, "destination was created")
	}
	if os.IsNotExist(err) {
		return configChangedError(file.path, "destination was deleted")
	}
	if err != nil {
		return configChangedError(file.path, "destination cannot be accessed")
	}
	if !info.Mode().IsRegular() {
		return configChangedError(file.path, "destination type changed")
	}
	contents, err := os.ReadFile(file.path)
	if err != nil {
		return configChangedError(file.path, "destination cannot be read")
	}
	if !bytes.Equal(contents, file.original) {
		return configChangedError(file.path, "destination contents changed")
	}
	return nil
}

func configChangedError(path, reason string) error {
	return fmt.Errorf("config changed after preflight (%s): %s; rerun the installer", reason, path)
}

func commandError(output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}

type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (runner ExecRunner) Run(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout = runner.Stdout
	command.Stderr = runner.Stderr
	return command.Run()
}

func (runner ExecRunner) Output(name string, args ...string) ([]byte, error) {
	command := exec.Command(name, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.Bytes(), err
}
