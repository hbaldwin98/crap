package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestAnalyzeCodeToolReturnsStructuredReport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\nfunc Work(ok bool) { if ok { return } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.ts"), []byte("export const choose = (ok: boolean) => ok ? 1 : 0;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "excluded.go"), []byte("package sample\nfunc Excluded() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New("test", policy).Connect(ctx, serverTransport, nil)
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
	if len(tools.Tools) != 2 || tools.Tools[0].Name != "analyze_code" || tools.Tools[1].Name != "get_analysis_results" {
		t.Fatalf("tools = %#v, want analyze_code and get_analysis_results", tools.Tools)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "analyze_code",
		Arguments: map[string]any{
			"root":       root,
			"paths":      []string{"."},
			"exclude":    []string{"excluded.go"},
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
	languages := make(map[string]int)
	for _, method := range report.Methods {
		languages[method.Language] = method.Complexity
	}
	if len(report.Methods) != 2 || languages["go"] != 2 || languages["typescript"] != 2 {
		t.Fatalf("unexpected structured report: %#v", report)
	}
	if report.Page.TotalMatched != 2 || report.Page.Returned != 2 || report.Page.HasMore {
		t.Fatalf("unexpected page: %#v", report.Page)
	}
	if report.SchemaVersion != "6" || report.PageSchemaVersion != "4" || report.ReportID == "" || report.ExpiresAt == "" || report.Discovery.Selected != 2 {
		t.Fatalf("unexpected contract or discovery metadata: %#v", report)
	}
	if len(report.Discovery.Exclusions) != 1 || report.Discovery.Exclusions[0].Reason != "explicit" {
		t.Fatalf("exclude input was not propagated: %#v", report.Discovery)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate analysis page schema")
	}
	schema, err := jsonschema.NewCompiler().Compile(filepath.Join(filepath.Dir(sourceFile), "..", "..", "schemas", "v1", "analysis-mcp-page-v4.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(result.StructuredContent); err != nil {
		t.Fatalf("generated page does not validate against v4: %v", err)
	}

	if err := clientSession.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serverSession.Wait(); err != nil {
		t.Fatal(err)
	}
}

type fakeAnalyzer struct {
	calls  *atomic.Int32
	report analysis.Report
}

func (analyzer *fakeAnalyzer) AnalyzeContext(ctx context.Context, _ analysis.Options) (analysis.Report, error) {
	analyzer.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return analysis.Report{}, err
	}
	return analyzer.report, nil
}

func (*fakeAnalyzer) Close() {}

func TestAnalysisSnapshotRunsOnceAndCursorIsBound(t *testing.T) {
	root := t.TempDir()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	report := analysis.Report{SchemaVersion: "6", ReportType: "analysis", Methods: []analysis.MethodResult{{ID: "a", CRAP: 20}, {ID: "b", CRAP: 10}}}
	store := newSnapshotStore()
	factory := func() (analyzerExecution, error) { return &fakeAnalyzer{calls: calls, report: report}, nil }
	_, first, err := analyzeWith(context.Background(), nil, AnalyzeInput{Root: root, ResultMode: "all", Limit: intPointer(1)}, policy, store, factory)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(first.Methods) != 1 || first.Page.NextCursor == "" {
		t.Fatalf("first page = %#v, calls = %d", first, calls.Load())
	}
	source := filepath.Join(root, "source.go")
	if err := os.WriteFile(source, []byte("package source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	report.Methods[1].ID = "changed-after-snapshot"
	second, err := getAnalysisResults(context.Background(), store, GetResultsInput{Cursor: first.Page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(second.Methods) != 1 || second.Methods[0].ID != "b" || second.ReportID != first.ReportID {
		t.Fatalf("second page = %#v, calls = %d", second, calls.Load())
	}
	replacement := "A"
	if strings.HasSuffix(first.Page.NextCursor, replacement) {
		replacement = "B"
	}
	tampered := first.Page.NextCursor[:len(first.Page.NextCursor)-1] + replacement
	if _, err := getAnalysisResults(context.Background(), store, GetResultsInput{Cursor: tampered}); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
	if _, err := getAnalysisResults(context.Background(), store, GetResultsInput{Cursor: first.Page.NextCursor, ReportID: first.ReportID}); err == nil {
		t.Fatal("cursor was accepted with a mixed query")
	}
}

func TestAnalyzeOffsetRemainsCompatible(t *testing.T) {
	root := t.TempDir()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	report := analysis.Report{SchemaVersion: "6", Methods: []analysis.MethodResult{{ID: "a", CRAP: 20}, {ID: "b", CRAP: 10}}}
	store := newSnapshotStore()
	factory := func() (analyzerExecution, error) { return &fakeAnalyzer{calls: &atomic.Int32{}, report: report}, nil }
	_, page, err := analyzeWith(context.Background(), nil, AnalyzeInput{Root: root, ResultMode: "all", Limit: intPointer(1), Offset: 1}, policy, store, factory)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Methods) != 1 || page.Methods[0].ID != "b" || page.Page.Offset != 1 {
		t.Fatalf("offset page = %#v", page)
	}
}

func TestAnalysisSnapshotIsImmutableAndExpires(t *testing.T) {
	store := newSnapshotStore()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	report := analysis.Report{SchemaVersion: "6", Methods: []analysis.MethodResult{{ID: "original"}}}
	item, err := store.put(report)
	if err != nil {
		t.Fatal(err)
	}
	report.Methods[0].ID = "mutated"
	page, err := getAnalysisResults(context.Background(), store, GetResultsInput{ReportID: item.id, ResultMode: "all"})
	if err != nil || page.Methods[0].ID != "original" {
		t.Fatalf("snapshot changed: %#v, %v", page, err)
	}
	now = now.Add(defaultSnapshotTTL)
	if _, err := getAnalysisResults(context.Background(), store, GetResultsInput{ReportID: item.id}); err == nil {
		t.Fatal("expired snapshot was returned")
	}
}

func TestCanceledSnapshotIsNotRetained(t *testing.T) {
	store := newSnapshotStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.putContext(ctx, analysis.Report{}); err == nil {
		t.Fatal("canceled snapshot was retained")
	}
	if len(store.snapshots) != 0 || store.totalBytes != 0 {
		t.Fatalf("canceled snapshot changed store: %#v", store)
	}
}

func intPointer(value int) *int { return &value }

func TestCompactReportDefaultsToPagedViolations(t *testing.T) {
	methods := []analysis.MethodResult{
		{ID: "low", CRAP: 2},
		{ID: "second", CRAP: 35, AboveThreshold: true},
		{ID: "first", CRAP: 50, AboveThreshold: true},
	}
	report := analysis.Report{
		SchemaVersion: "6", DiffBase: "main", DiffBaseCommit: "base", DiffHeadCommit: "head", DiffMergeBase: "merge",
		Threshold: 30, Summary: analysis.Summary{Methods: 3, AboveThreshold: 2}, Methods: methods,
		Diagnostics: []analysis.Diagnostic{{Severity: "warning", Code: "coverage-path-suffix-matched", Path: "work.ts"}},
	}

	first := compactReport(report, "violations", 1, 0)
	if len(first.Methods) != 1 || first.Methods[0].ID != "first" || first.Page.TotalMatched != 2 || !first.Page.HasMore {
		t.Fatalf("unexpected first page: %#v", first)
	}
	if first.SchemaVersion != "6" || first.PageSchemaVersion != "3" || first.DiffBaseCommit != "base" || first.DiffHeadCommit != "head" || first.DiffMergeBase != "merge" || len(first.Diagnostics) != 1 {
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

func TestCompactReportAlwaysSerializesDiagnostics(t *testing.T) {
	output := compactReport(analysis.Report{Methods: []analysis.MethodResult{}, Diagnostics: []analysis.Diagnostic{}}, "summary", 20, 0)
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"diagnostics":[]`) {
		t.Fatalf("empty diagnostics missing from MCP contract: %s", data)
	}
}

func TestAnalyzeRejectsRootOutsidePolicy(t *testing.T) {
	root := t.TempDir()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := analyze(context.Background(), nil, AnalyzeInput{Root: t.TempDir()}, policy); err == nil {
		t.Fatal("outside root was accepted")
	}
}
