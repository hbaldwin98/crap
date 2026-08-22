package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func testPaths() map[string]string {
	return map[string]string{"crap": filepath.Join("root with spaces", "crap"), "crap-mutate": filepath.Join("root with spaces", "crap-mutate")}
}

func openCodeTestSystem(env map[string]string) System {
	return System{Getenv: func(key string) string { return env[key] }}
}

func TestOpenCodeCreationAndJSONEscaping(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "nested")
	path := filepath.Join(directory, "opencode.json")
	paths := testPaths()
	paths["crap"] = `C:\Program Files\crap\crap.exe`
	files, err := prepareOpenCode(openCodeTestSystem(map[string]string{"OPENCODE_CONFIG": path}), t.TempDir(), paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := commitFile(files[0]); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCP map[string]openCodeServer `json:"mcp"`
	}
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatal(err)
	}
	got := config.MCP["crap"]
	if got.Type != "local" || !got.Enabled || got.Command[0] != paths["crap"] || got.Command[1] != "mcp" {
		t.Fatalf("config = %s", contents)
	}
}

func TestOpenCodeAcceptsEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := prepareOpenCode(openCodeTestSystem(map[string]string{"OPENCODE_CONFIG": path}), t.TempDir(), testPaths())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.Contains(string(files[0].contents), `"crap-mutate"`) {
		t.Fatalf("prepared files = %#v", files)
	}
}

func TestConfigPatchPreservesCommentsSettingsAndOtherServers(t *testing.T) {
	input := `{
  // Keep this comment.
  "theme": "dark",
  "mcp": {
    "other": { "type": "remote", "url": "https://example.test" },
    // Old managed entry.
    "crap": { "type": "local", "command": ["old", "mcp"], "enabled": false }
  }
}`
	servers := make(map[string]any)
	for name, server := range openCodeServers(testPaths()) {
		servers[name] = server
	}
	updated, err := patchConfig([]byte(input), "mcp", servers)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	for _, retained := range []string{"Keep this comment.", `"theme": "dark"`, `"other"`} {
		if !strings.Contains(text, retained) {
			t.Fatalf("unrelated config lost:\n%s", text)
		}
	}
	if strings.Contains(text, `"old"`) || !strings.Contains(text, `"crap-mutate"`) {
		t.Fatalf("managed servers not replaced:\n%s", text)
	}
	value, err := hujson.Parse(updated)
	if err != nil {
		t.Fatal(err)
	}
	value.Standardize()
	var config map[string]any
	if err := json.Unmarshal(value.Pack(), &config); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPatchRejectsDuplicateObjectMembers(t *testing.T) {
	tests := []struct {
		name      string
		serverKey string
		input     string
		want      string
	}{
		{"OpenCode parent", "mcp", `{"mcp":{},"mcp":{}}`, `duplicate object member "mcp" at /`},
		{"OpenCode server", "mcp", `{"mcp":{"crap":{},"crap":{}}}`, `duplicate object member "crap" at /mcp`},
		{"OpenCode nested server field", "mcp", `{"mcp":{"other":{"command":[],"command":[]}}}`, `duplicate object member "command" at /mcp/other`},
		{"generic parent", "mcpServers", `{"mcpServers":{},"mcpServers":{}}`, `duplicate object member "mcpServers" at /`},
		{"generic server", "mcpServers", `{"mcpServers":{"crap":{},"crap":{}}}`, `duplicate object member "crap" at /mcpServers`},
		{"generic nested server field", "mcpServers", `{"mcpServers":{"other":{"args":[],"args":[]}}}`, `duplicate object member "args" at /mcpServers/other`},
		{"object nested in array", "mcp", `{"items":[{"name":"one","name":"two"}]}`, `duplicate object member "name" at /items/0`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := patchConfig([]byte(test.input), test.serverKey, map[string]any{
				"crap": map[string]any{"command": "new"}, "crap-mutate": map[string]any{"command": "new"},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestOpenCodePreparesBothExistingFilesInLoadOrder(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(directory, "opencode.json")
	jsoncPath := filepath.Join(directory, "opencode.jsonc")
	if err := os.WriteFile(jsonPath, []byte(`{"mcp":{"crap":{"command":["first"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsoncPath, []byte("{ // later definition\n\"mcp\":{\"crap\":{\"command\":[\"second\"]}}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := prepareOpenCode(openCodeTestSystem(nil), home, testPaths())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].path != jsonPath || files[1].path != jsoncPath {
		t.Fatalf("prepared files = %#v", files)
	}
	for _, file := range files {
		if strings.Contains(string(file.contents), `"first"`) || strings.Contains(string(file.contents), `"second"`) {
			t.Fatalf("stale definition retained in %s: %s", file.path, file.contents)
		}
	}
	if !strings.Contains(string(files[1].contents), "later definition") {
		t.Fatalf("jsonc comment lost: %s", files[1].contents)
	}
	var stdout strings.Builder
	printDryRun(plan{
		options: Options{DryRun: true, Stdout: &stdout}, paths: testPaths(),
		generic: preparedFile{path: filepath.Join(home, "generic.json")}, openCode: files,
	}, []string{"install"})
	jsonReport := "Would update OpenCode config: " + jsonPath
	jsoncReport := "Would update OpenCode config: " + jsoncPath
	if first, second := strings.Index(stdout.String(), jsonReport), strings.Index(stdout.String(), jsoncReport); first < 0 || second <= first {
		t.Fatalf("config paths not reported in load order:\n%s", stdout.String())
	}
}

func TestOpenCodeConfigOverridesAndFileChoice(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"OPENCODE_CONFIG": filepath.Join(home, "custom.jsonc")}, filepath.Join(home, "custom.jsonc")},
		{map[string]string{"OPENCODE_CONFIG_DIR": filepath.Join(home, "dir")}, filepath.Join(home, "dir", "opencode.json")},
		{map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, "xdg")}, filepath.Join(home, "xdg", "opencode", "opencode.json")},
	}
	for _, test := range tests {
		got, err := openCodePaths(openCodeTestSystem(test.env), home)
		if err != nil || len(got) != 1 || got[0] != test.want {
			t.Fatalf("paths=%q want=%q err=%v", got, test.want, err)
		}
	}
}

func TestOpenCodeDestinationSymlinkRejectedDuringPrepare(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated privileges")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	path := filepath.Join(directory, "opencode.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	_, err := prepareOpenCode(openCodeTestSystem(map[string]string{"OPENCODE_CONFIG": path}), t.TempDir(), testPaths())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v", err)
	}
}
