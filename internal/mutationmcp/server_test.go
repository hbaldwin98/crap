package mutationmcp

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/hbaldwin98/crap/internal/rootauth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeExecutor struct {
	received mutation.Options
	report   mutation.Report
	err      error
}

func (executor *fakeExecutor) Run(_ context.Context, options mutation.Options, _ io.Writer) (mutation.Report, error) {
	executor.received = options
	if executor.report.SchemaVersion != "" || executor.err != nil {
		return executor.report, executor.err
	}
	score := 90.0
	return mutation.Report{
		SchemaVersion: "2", Engine: "stryker-js", Language: "typescript", Score: &score, Passed: true,
		Mutants: []mutation.MutantResult{
			{ID: "1", File: "src/work.ts", Status: "killed"},
			{ID: "2", File: "src/work.ts", Status: "survived"},
			{ID: "3", File: "src/work.ts", Status: "noCoverage"},
		},
	}, nil
}

func policyFor(t *testing.T, root string) *rootauth.Policy {
	t.Helper()
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestRunMutationTestsToolReturnsPagedStructuredReport(t *testing.T) {
	root := t.TempDir()
	executor := &fakeExecutor{}
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newServer("test", executor, policyFor(t, root), newSnapshotStore()).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if instructions := clientSession.InitializeResult().Instructions; !strings.Contains(instructions, "never infer") || !strings.Contains(instructions, "not sandboxed") {
		t.Fatalf("instructions = %q", instructions)
	}
	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 || tools.Tools[0].Name != "get_mutation_results" || tools.Tools[1].Name != "run_mutation_tests" {
		t.Fatalf("tools = %#v", tools.Tools)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "run_mutation_tests", Arguments: map[string]any{
		"language": "typescript", "paths": []string{"src/work.ts"}, "minimumScore": 85, "limit": 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output MutationOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if output.Engine != "stryker-js" || output.ReportID == "" || len(output.Mutants) != 1 || output.Mutants[0].Status != "survived" || output.Page.NextCursor == "" {
		t.Fatalf("unexpected output: %#v", output)
	}
	if executor.received.MinimumScore != 85 || executor.received.Paths[0] != "src/work.ts" || executor.received.Authorization == nil {
		t.Fatalf("options = %#v", executor.received)
	}
	second, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "get_mutation_results", Arguments: map[string]any{"cursor": output.Page.NextCursor}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(second.StructuredContent)
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Mutants) != 1 || output.Mutants[0].Status != "noCoverage" || output.Page.HasMore {
		t.Fatalf("second page = %#v", output)
	}
	if err := clientSession.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serverSession.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestRunMutationDefaultsRootAndAcceptsScoreBoundaries(t *testing.T) {
	root := t.TempDir()
	policy := policyFor(t, root)
	for _, minimum := range []float64{0, 100} {
		executor := &fakeExecutor{}
		_, _, err := runMutation(context.Background(), executor, policy, newSnapshotStore(), RunInput{Language: "go", MinimumScore: &minimum})
		if err != nil {
			t.Fatalf("minimum %v: %v", minimum, err)
		}
		if executor.received.Root != root || executor.received.MinimumScore != minimum {
			t.Fatalf("options = %#v", executor.received)
		}
	}
}

func TestRunMutationRejectsInvalidScoresAndOutsideRoots(t *testing.T) {
	root := t.TempDir()
	policy := policyFor(t, root)
	for _, minimum := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		executor := &fakeExecutor{}
		if _, _, err := runMutation(context.Background(), executor, policy, newSnapshotStore(), RunInput{Language: "go", MinimumScore: &minimum}); err == nil {
			t.Errorf("minimum %v was accepted", minimum)
		}
	}
	executor := &fakeExecutor{}
	if _, _, err := runMutation(context.Background(), executor, policy, newSnapshotStore(), RunInput{Root: t.TempDir(), Language: "go"}); err == nil {
		t.Fatal("outside root was accepted")
	}
	if executor.received.Root != "" {
		t.Fatal("executor ran for unauthorized root")
	}
}

func TestSnapshotQueriesAreImmutableAndExpire(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	store := newSnapshotStore()
	store.now = func() time.Time { return now }
	report := (&fakeExecutor{}).report
	score := 90.0
	report = mutation.Report{SchemaVersion: "2", Score: &score, Mutants: []mutation.MutantResult{{ID: "1", Status: "survived"}}}
	item, err := store.put(report)
	if err != nil {
		t.Fatal(err)
	}
	query, _ := normalizeQuery("all", nil, nil)
	first := pageReport(store, report, item, query, 0)
	first.Mutants[0].Status = "killed"
	stored, err := getMutationResults(store, GetResultsInput{ReportID: item.id, ResultMode: "all"})
	if err != nil || stored.Mutants[0].Status != "survived" {
		t.Fatalf("stored output = %#v, %v", stored, err)
	}
	now = now.Add(defaultSnapshotTTL)
	if _, err := getMutationResults(store, GetResultsInput{ReportID: item.id}); err == nil {
		t.Fatal("expired snapshot was returned")
	}
}

func TestNormalizeQueryValidatesModesStatusesAndLimits(t *testing.T) {
	zero := 0
	if _, err := normalizeQuery("bad", nil, nil); err == nil {
		t.Fatal("bad mode accepted")
	}
	if _, err := normalizeQuery("summary", []string{"survived"}, nil); err == nil {
		t.Fatal("summary status accepted")
	}
	if _, err := normalizeQuery("all", []string{"unknown"}, nil); err == nil {
		t.Fatal("unknown status accepted")
	}
	if _, err := normalizeQuery("all", nil, &zero); err == nil {
		t.Fatal("zero limit accepted")
	}
}

func TestSnapshotCursorRejectsTampering(t *testing.T) {
	store := newSnapshotStore()
	cursor := store.encodeCursor(cursorState{Version: 1, ReportID: "report", Offset: 20, ResultMode: "all", Limit: 20})
	last := cursor[len(cursor)-1]
	replacement := byte('A')
	if last == replacement {
		replacement = 'B'
	}
	if _, err := store.decodeCursor(cursor[:len(cursor)-1] + string(replacement)); err == nil {
		t.Fatal("tampered cursor was accepted")
	}
}
