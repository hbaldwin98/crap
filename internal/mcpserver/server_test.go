package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAnalyzeCodeToolReturnsStructuredReport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\nfunc Work(ok bool) { if ok { return } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.ts"), []byte("export const choose = (ok: boolean) => ok ? 1 : 0;\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New("test").Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if instructions := clientSession.InitializeResult().Instructions; !strings.Contains(instructions, "Never estimate") {
		t.Fatalf("server instructions = %q", instructions)
	}

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "analyze_code" {
		t.Fatalf("tools = %#v, want analyze_code", tools.Tools)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "analyze_code",
		Arguments: map[string]any{
			"root":       root,
			"paths":      []string{"sample.go", "sample.ts"},
			"resultMode": "all",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned an error: %#v", result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var report AnalyzeOutput
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Methods) != 2 || report.Methods[0].Language != "go" || report.Methods[1].Language != "typescript" || report.Methods[1].Complexity != 2 {
		t.Fatalf("unexpected structured report: %#v", report)
	}
	if report.Page.TotalMatched != 2 || report.Page.Returned != 2 || report.Page.HasMore {
		t.Fatalf("unexpected page: %#v", report.Page)
	}

	if err := clientSession.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serverSession.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCompactReportDefaultsToPagedViolations(t *testing.T) {
	methods := []analysis.MethodResult{
		{ID: "low", CRAP: 2},
		{ID: "second", CRAP: 35, AboveThreshold: true},
		{ID: "first", CRAP: 50, AboveThreshold: true},
	}
	report := analysis.Report{
		SchemaVersion: "3", DiffBase: "main", DiffBaseCommit: "base", DiffHeadCommit: "head", DiffMergeBase: "merge",
		Threshold: 30, Summary: analysis.Summary{Methods: 3, AboveThreshold: 2}, Methods: methods,
		Diagnostics: []analysis.Diagnostic{{Severity: "warning", Code: "coverage-path-suffix-matched", Path: "work.ts"}},
	}

	first := compactReport(report, "violations", 1, 0)
	if len(first.Methods) != 1 || first.Methods[0].ID != "first" || first.Page.TotalMatched != 2 || !first.Page.HasMore || first.Page.NextOffset == nil || *first.Page.NextOffset != 1 {
		t.Fatalf("unexpected first page: %#v", first)
	}
	if first.SchemaVersion != "3" || first.DiffBaseCommit != "base" || first.DiffHeadCommit != "head" || first.DiffMergeBase != "merge" || len(first.Diagnostics) != 1 {
		t.Fatalf("diff metadata was not preserved: %#v", first)
	}
	second := compactReport(report, "violations", 1, 1)
	if len(second.Methods) != 1 || second.Methods[0].ID != "second" || second.Page.HasMore {
		t.Fatalf("unexpected second page: %#v", second)
	}
	summary := compactReport(report, "summary", 20, 0)
	if len(summary.Methods) != 0 || summary.Page.TotalMatched != 0 {
		t.Fatalf("unexpected summary page: %#v", summary)
	}
}

func TestResultModeAcceptsOnlyDocumentedModes(t *testing.T) {
	for _, mode := range []string{"summary", "violations", "highest", "all"} {
		got, err := resultMode(mode)
		if err != nil || got != mode {
			t.Errorf("resultMode(%q) = %q, %v", mode, got, err)
		}
	}
	if _, err := resultMode("everything"); err == nil {
		t.Fatal("expected unsupported result mode error")
	}
}

func TestCompactReportSortsEqualScoresByID(t *testing.T) {
	report := analysis.Report{Methods: []analysis.MethodResult{
		{ID: "second", CRAP: 10},
		{ID: "first", CRAP: 10},
	}}
	result := compactReport(report, "all", 20, 0)
	if len(result.Methods) != 2 || result.Methods[0].ID != "first" || result.Methods[1].ID != "second" {
		t.Fatalf("methods = %#v", result.Methods)
	}
}
