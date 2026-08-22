package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type invocation struct {
	name string
	args []string
}

type fakeRunner struct {
	outputs map[string][]byte
	errors  map[string]error
	calls   []invocation
	onRun   func(invocation) error
}

func commandKey(name string, args []string) string { return name + "\x00" + strings.Join(args, "\x00") }

func (runner *fakeRunner) Run(name string, args ...string) error {
	call := invocation{name: name, args: append([]string(nil), args...)}
	runner.calls = append(runner.calls, call)
	if runner.onRun != nil {
		return runner.onRun(call)
	}
	return runner.errors[commandKey(name, args)]
}

func (runner *fakeRunner) Output(name string, args ...string) ([]byte, error) {
	call := invocation{name: name, args: append([]string(nil), args...)}
	runner.calls = append(runner.calls, call)
	key := commandKey(name, args)
	return runner.outputs[key], runner.errors[key]
}

func systemFor(t *testing.T, runner *fakeRunner, home, bin string) System {
	t.Helper()
	if runner.outputs == nil {
		runner.outputs = make(map[string][]byte)
	}
	runner.outputs[commandKey("go", []string{"env", "GOOS"})] = []byte(runtime.GOOS)
	runner.outputs[commandKey("go", []string{"env", "GOARCH"})] = []byte(runtime.GOARCH)
	runner.outputs[commandKey("go", []string{"env", "GOBIN"})] = []byte(bin)
	return System{
		Runner: runner,
		LookPath: func(name string) (string, error) {
			return "", execNotFound(name)
		},
		Getenv:  func(string) string { return "" },
		HomeDir: func() (string, error) { return home, nil },
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
}

type execNotFound string

func (err execNotFound) Error() string { return string(err) + " not found" }

func hasInstallCall(calls []invocation) bool {
	for _, call := range calls {
		if call.name == "go" && len(call.args) > 0 && call.args[0] == "install" {
			return true
		}
	}
	return false
}

func createInstalledBinaries(bin string) error {
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	for _, path := range binaryPaths(bin, runtime.GOOS) {
		if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func TestRunInstallsBothCommandsAndReportsCompletedSteps(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "bin with spaces")
	runner := &fakeRunner{}
	runner.onRun = func(call invocation) error {
		return createInstalledBinaries(bin)
	}
	var stdout strings.Builder
	err := Run(systemFor(t, runner, home, bin), Options{Version: "v1.2.3", Clients: []string{"generic"}, Stdout: &stdout})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"install", module + "/cmd/crap@v1.2.3", module + "/cmd/crap-mutate@v1.2.3"}
	if len(runner.calls) != 4 || !reflect.DeepEqual(runner.calls[3].args, wantArgs) {
		t.Fatalf("calls = %#v, want install args %#v", runner.calls, wantArgs)
	}
	paths := binaryPaths(bin, runtime.GOOS)
	genericPath := filepath.Join(home, ".config", "crap", "mcp.json")
	wantOutput := fmt.Sprintf("Installed binaries: %s, %s\nWrote generic MCP config: %s\n", paths["crap"], paths["crap-mutate"], genericPath)
	if stdout.String() != wantOutput {
		t.Fatalf("stdout = %q, want %q", stdout.String(), wantOutput)
	}
	contents, err := os.ReadFile(genericPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]genericServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(contents, &config); err != nil || config.MCPServers["crap"].Command != paths["crap"] {
		t.Fatalf("generic config = %s, err=%v", contents, err)
	}
}

func TestConcurrentGenericChangeIsNotOverwritten(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".config", "crap", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"before"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"mcpServers":{"other":{"command":"changed during install"}}}`)
	runner := &fakeRunner{}
	runner.onRun = func(invocation) error {
		if err := createInstalledBinaries(bin); err != nil {
			return err
		}
		return os.WriteFile(path, changed, 0o600)
	}
	var stdout strings.Builder
	err := Run(systemFor(t, runner, home, bin), Options{Version: "latest", Clients: []string{"generic"}, Stdout: &stdout})
	if err == nil || !strings.Contains(err.Error(), "contents changed") || !strings.Contains(err.Error(), "rerun") {
		t.Fatalf("error = %v", err)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil || !reflect.DeepEqual(contents, changed) {
		t.Fatalf("config=%q read error=%v", contents, readErr)
	}
	paths := binaryPaths(bin, runtime.GOOS)
	want := fmt.Sprintf("Installed binaries: %s, %s\n", paths["crap"], paths["crap-mutate"])
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestConcurrentOpenCodeChangeIsNotOverwritten(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcp":{"other":{"type":"local"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"mcp":{"other":{"type":"changed during install"}}}`)
	runner := &fakeRunner{}
	runner.onRun = func(invocation) error {
		if err := createInstalledBinaries(bin); err != nil {
			return err
		}
		return os.WriteFile(path, changed, 0o600)
	}
	var stdout strings.Builder
	err := Run(systemFor(t, runner, home, bin), Options{Version: "latest", Clients: []string{"opencode"}, Stdout: &stdout})
	if err == nil || !strings.Contains(err.Error(), "contents changed") || !strings.Contains(err.Error(), "rerun") {
		t.Fatalf("error = %v", err)
	}
	contents, readErr := os.ReadFile(path)
	if readErr != nil || !reflect.DeepEqual(contents, changed) {
		t.Fatalf("config=%q read error=%v", contents, readErr)
	}
	paths := binaryPaths(bin, runtime.GOOS)
	genericPath := filepath.Join(home, ".config", "crap", "mcp.json")
	if _, statErr := os.Stat(genericPath); !os.IsNotExist(statErr) {
		t.Fatalf("generic config was written before all destinations were verified: %v", statErr)
	}
	want := fmt.Sprintf("Installed binaries: %s, %s\n", paths["crap"], paths["crap-mutate"])
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestBinaryDirectoryUsesGOBINThenFirstGOPATHEntry(t *testing.T) {
	t.Run("GOBIN", func(t *testing.T) {
		runner := &fakeRunner{outputs: map[string][]byte{commandKey("go", []string{"env", "GOBIN"}): []byte(t.TempDir())}}
		got, err := binaryDirectory(System{Runner: runner, GOOS: runtime.GOOS})
		if err != nil || !filepath.IsAbs(got) || len(runner.calls) != 1 {
			t.Fatalf("directory=%q calls=%v err=%v", got, runner.calls, err)
		}
	})
	t.Run("GOPATH", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		runner := &fakeRunner{outputs: map[string][]byte{
			commandKey("go", []string{"env", "GOBIN"}):  nil,
			commandKey("go", []string{"env", "GOPATH"}): []byte(first + string(os.PathListSeparator) + second),
		}}
		got, err := binaryDirectory(System{Runner: runner, GOOS: runtime.GOOS})
		if err != nil || got != filepath.Join(first, "bin") {
			t.Fatalf("directory=%q err=%v", got, err)
		}
	})
	if got := firstGoPath("/one:/two", "linux"); got != "/one" {
		t.Fatalf("Unix GOPATH first entry = %q", got)
	}
	if got := firstGoPath(`C:\one;D:\two`, "windows"); got != `C:\one` {
		t.Fatalf("Windows GOPATH first entry = %q", got)
	}
	if !strings.HasSuffix(binaryPaths(`C:\Go Bin`, "windows")["crap"], ".exe") {
		t.Fatal("Windows binary lacks .exe suffix")
	}
}

func TestSelectedClientsDetectionAndNormalization(t *testing.T) {
	system := System{LookPath: func(name string) (string, error) {
		if name == "claude" {
			return "/bin/claude", nil
		}
		return "", errors.New("missing")
	}}
	got, err := selectedClients(system, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"claude"}) {
		t.Fatalf("detected=%v err=%v", got, err)
	}
	got, err = selectedClients(system, []string{"opencode,generic", "claude", "opencode"})
	if err != nil || !reflect.DeepEqual(got, []string{"opencode", "claude"}) {
		t.Fatalf("explicit=%v err=%v", got, err)
	}
	if _, err := selectedClients(system, []string{"cursor"}); err == nil {
		t.Fatal("unsupported client accepted")
	}
}

func TestExplicitClaudeRequiresCLI(t *testing.T) {
	runner := &fakeRunner{}
	system := systemFor(t, runner, t.TempDir(), t.TempDir())
	err := Run(system, Options{Version: "latest", Clients: []string{"claude"}, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "claude") || hasInstallCall(runner.calls) {
		t.Fatalf("error=%v calls=%#v", err, runner.calls)
	}
}

func TestDryRunHasNoSideEffectsAndExactQuotedOutput(t *testing.T) {
	home, bin := t.TempDir(), filepath.Join(t.TempDir(), "missing bin")
	runner := &fakeRunner{}
	system := systemFor(t, runner, home, bin)
	system.LookPath = func(name string) (string, error) { return filepath.Join("tools with spaces", name), nil }
	var stdout strings.Builder
	if err := Run(system, Options{Version: "latest", Clients: []string{"claude", "opencode"}, DryRun: true, Stdout: &stdout}); err != nil {
		t.Fatal(err)
	}
	if hasInstallCall(runner.calls) {
		t.Fatalf("dry-run installed: %#v", runner.calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created config directory: %v", err)
	}
	paths := binaryPaths(bin, runtime.GOOS)
	install := formatArgv([]string{"go", "install", module + "/cmd/crap@latest", module + "/cmd/crap-mutate@latest"})
	claude := filepath.Join("tools with spaces", "claude")
	wantLines := []string{
		"Would run: " + install,
		"Would write generic MCP config: " + filepath.Join(home, ".config", "crap", "mcp.json"),
		"Would update OpenCode config: " + filepath.Join(home, ".config", "opencode", "opencode.json"),
		"Would run: " + formatArgv(append([]string{claude}, claudeAddArgs("crap", paths["crap"])...)),
		"Would run: " + formatArgv(append([]string{claude}, claudeAddArgs("crap-mutate", paths["crap-mutate"])...)),
	}
	if got, want := strings.TrimSpace(stdout.String()), strings.Join(wantLines, "\n"); got != want {
		t.Fatalf("dry-run output:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenericPatchPreservesCommentsPropertiesAndOtherServers(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "mcp.json")
	input := `{
  // retained comment
  "setting": true,
  "mcpServers": {
    "other": {"command": "other"},
    "crap": {"command": "stale"}
  }
}`
	if err := os.WriteFile(path, []byte(input), 0o640); err != nil {
		t.Fatal(err)
	}
	file, err := prepareGeneric(path, testPaths())
	if err != nil {
		t.Fatal(err)
	}
	text := string(file.contents)
	for _, retained := range []string{"retained comment", `"setting": true`, `"other"`} {
		if !strings.Contains(text, retained) {
			t.Fatalf("generic config lost %q:\n%s", retained, text)
		}
	}
	if strings.Contains(text, `"stale"`) {
		t.Fatalf("stale managed entry retained:\n%s", text)
	}
	if err := commitFile(file); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("mode = %o, want 640", info.Mode().Perm())
		}
	}
}

func TestNewConfigIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX permission bits consistently")
	}
	path := filepath.Join(t.TempDir(), "new", "mcp.json")
	file, err := prepareGeneric(path, testPaths())
	if err != nil {
		t.Fatal(err)
	}
	if err := commitFile(file); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestMalformedConfigPreflightPreventsInstall(t *testing.T) {
	tests := []struct {
		name    string
		client  string
		prepare func(home string) error
	}{
		{"generic", "generic", func(home string) error {
			path := filepath.Join(home, ".config", "crap", "mcp.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("{"), 0o600)
		}},
		{"opencode", "opencode", func(home string) error {
			path := filepath.Join(home, ".config", "opencode", "opencode.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("{"), 0o600)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			if err := test.prepare(home); err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{}
			err := Run(systemFor(t, runner, home, bin), Options{Version: "latest", Clients: []string{test.client}, Stdout: io.Discard})
			if err == nil || hasInstallCall(runner.calls) {
				t.Fatalf("error=%v calls=%#v", err, runner.calls)
			}
		})
	}
}

func TestGenericSymlinkPreflightPreventsInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	home, bin := t.TempDir(), t.TempDir()
	directory := filepath.Join(home, ".config", "crap")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "mcp.json")); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	err := Run(systemFor(t, runner, home, bin), Options{Version: "latest", Clients: []string{"generic"}, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "symlink") || hasInstallCall(runner.calls) {
		t.Fatalf("error=%v calls=%#v", err, runner.calls)
	}
}

func TestOpenCodeSymlinkPreflightPreventsInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	home, bin := t.TempDir(), t.TempDir()
	directory := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "target")
	path := filepath.Join(directory, "opencode.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	system := systemFor(t, runner, home, bin)
	system.Getenv = func(key string) string {
		if key == "OPENCODE_CONFIG" {
			return path
		}
		return ""
	}
	err := Run(system, Options{Version: "latest", Clients: []string{"opencode"}, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "symlink") || hasInstallCall(runner.calls) {
		t.Fatalf("error=%v calls=%#v", err, runner.calls)
	}
}

func TestGoTargetMismatchPreventsInstall(t *testing.T) {
	for _, key := range []string{"GOOS", "GOARCH"} {
		t.Run(key, func(t *testing.T) {
			runner := &fakeRunner{}
			system := systemFor(t, runner, t.TempDir(), t.TempDir())
			runner.outputs[commandKey("go", []string{"env", key})] = []byte("different")
			err := Run(system, Options{Version: "latest", Clients: []string{"generic"}, Stdout: io.Discard})
			if err == nil || !strings.Contains(err.Error(), key) || hasInstallCall(runner.calls) {
				t.Fatalf("error=%v calls=%#v", err, runner.calls)
			}
		})
	}
}

func TestInstallErrorsWhenBinaryIsMissing(t *testing.T) {
	runner := &fakeRunner{}
	err := Run(systemFor(t, runner, t.TempDir(), t.TempDir()), Options{Version: "latest", Clients: []string{"generic"}, Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "verify installed crap") {
		t.Fatalf("error = %v", err)
	}
}

func TestGoEnvErrorIncludesOutput(t *testing.T) {
	key := commandKey("go", []string{"env", "GOOS"})
	runner := &fakeRunner{outputs: map[string][]byte{key: []byte("go failed")}, errors: map[string]error{key: errors.New("exit 1")}}
	err := validateGoTarget(System{Runner: runner, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if err == nil || !strings.Contains(err.Error(), "go failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestHomeError(t *testing.T) {
	runner := &fakeRunner{}
	system := systemFor(t, runner, "", t.TempDir())
	system.HomeDir = func() (string, error) { return "", fmt.Errorf("no home") }
	if err := Run(system, Options{}); err == nil || !strings.Contains(err.Error(), "no home") {
		t.Fatalf("error = %v", err)
	}
}
