package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hbaldwin98/crap/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPStdioRunsBuiltExecutable(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "sample.go"), []byte("package sample\nfunc Work() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	repository := filepath.Dir(filepath.Dir(filepath.Dir(sourceFile)))
	binary := filepath.Join(t.TempDir(), "crap")
	build := exec.Command("go", "build", "-o", binary, "./cmd/crap")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build MCP executable: %v\n%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	serverCommand := exec.Command(binary, "mcp", "--root", project)
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCommand}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if instructions := session.InitializeResult().Instructions; !strings.Contains(instructions, "Never estimate") {
		t.Fatalf("server instructions = %q", instructions)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []string{
		"analyze_code", "get_analysis_results", "analyze_change_scope", "get_change_scope",
		"compare_change_scope", "get_change_scope_comparison", "review_change", "get_review_change",
		"analyze_code_graph", "get_code_graph", "get_code_graph_neighborhood",
	}
	toolNames := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		toolNames[tool.Name] = true
	}
	if len(tools.Tools) != len(wantTools) {
		t.Fatalf("tool count = %d, want %d", len(tools.Tools), len(wantTools))
	}
	for _, name := range wantTools {
		if !toolNames[name] {
			t.Fatalf("missing MCP tool %q in %#v", name, toolNames)
		}
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "analyze_code",
		Arguments: map[string]any{
			"root": project, "paths": []string{"."}, "resultMode": "all",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("analyze_code returned an error: %#v", result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output mcpserver.AnalyzeOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.PageSchemaVersion != "4" || output.ReportID == "" || output.ExpiresAt == "" || len(output.Methods) != 1 || output.Summary.Methods != 1 {
		t.Fatalf("unexpected analysis output: %#v", output)
	}
	if output.Coverage.Format != "none" {
		t.Fatalf("coverage format = %q, want none", output.Coverage.Format)
	}
	for _, diagnostic := range output.Diagnostics {
		if diagnostic.Code == "coverage-not-provided" {
			return
		}
	}
	t.Fatalf("missing coverage diagnostic: %#v", output.Diagnostics)
}
