package installer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeExactArgv(t *testing.T) {
	paths := map[string]string{"crap": "/opt/CRAP Tools/crap", "crap-mutate": "/opt/CRAP Tools/crap-mutate"}
	runner := &fakeRunner{outputs: make(map[string][]byte)}
	var stdout strings.Builder
	if err := configureClaude(runner, "/usr/bin/claude", paths, &stdout); err != nil {
		t.Fatal(err)
	}
	want := invocation{name: "/usr/bin/claude", args: []string{
		"mcp", "add", "--scope", "user", "--transport", "stdio", "crap", "--", paths["crap"], "mcp",
	}}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if stdout.String() != "Added Claude Code server: crap\nAdded Claude Code server: crap-mutate\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestClaudeExistingDefinitionsAreNotRemoved(t *testing.T) {
	paths := map[string]string{"crap": "/bin/crap", "crap-mutate": "/bin/crap-mutate"}
	runner := &fakeRunner{outputs: make(map[string][]byte), errors: make(map[string]error)}
	for _, name := range serverNames {
		key := commandKey("claude", claudeAddArgs(name, paths[name]))
		runner.outputs[key] = []byte("MCP server " + name + " already exists in user config")
		runner.errors[key] = errors.New("exit 1")
	}
	var stdout strings.Builder
	if err := configureClaude(runner, "claude", paths, &stdout); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("destructive or retry calls = %#v", runner.calls)
	}
	want := "Claude Code server already exists; left unchanged: crap\nClaude Code server already exists; left unchanged: crap-mutate\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestClaudeDoesNotHideErrors(t *testing.T) {
	paths := map[string]string{"crap": "/bin/crap", "crap-mutate": "/bin/crap-mutate"}
	key := commandKey("claude", claudeAddArgs("crap", paths["crap"]))
	runner := &fakeRunner{
		outputs: map[string][]byte{key: []byte("permission denied")},
		errors:  map[string]error{key: errors.New("exit 1")},
	}
	err := configureClaude(runner, "claude", paths, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "permission denied") || len(runner.calls) != 1 {
		t.Fatalf("calls=%#v error=%v", runner.calls, err)
	}
}

func TestDryRunArgvQuotesPathWithSpaces(t *testing.T) {
	argv := append([]string{`C:\Program Files\Claude\claude.exe`}, claudeAddArgs("crap", `C:\Go Bin\crap.exe`)...)
	want := `"C:\\Program Files\\Claude\\claude.exe" "mcp" "add" "--scope" "user" "--transport" "stdio" "crap" "--" "C:\\Go Bin\\crap.exe" "mcp"`
	if got := formatArgv(argv); got != want {
		t.Fatalf("quoted argv = %q, want %q", got, want)
	}
}
