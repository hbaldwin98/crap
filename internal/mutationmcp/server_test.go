package mutationmcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeExecutor struct{ received mutation.Options }

func (executor *fakeExecutor) Run(_ context.Context, options mutation.Options, _ io.Writer) (mutation.Report, error) {
	executor.received = options
	score := 90.0
	return mutation.Report{SchemaVersion: "1", Engine: "stryker-js", Language: "typescript", Score: &score, Passed: true, Mutants: []mutation.MutantResult{}}, nil
}

func TestRunMutationTestsToolReturnsStructuredReport(t *testing.T) {
	executor := &fakeExecutor{}
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newServer("test", executor).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if instructions := clientSession.InitializeResult().Instructions; !strings.Contains(instructions, "never infer") {
		t.Fatalf("instructions = %q", instructions)
	}

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "run_mutation_tests" {
		t.Fatalf("tools = %#v", tools.Tools)
	}
	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "run_mutation_tests", Arguments: map[string]any{
		"root": t.TempDir(), "language": "typescript", "paths": []string{"src/work.ts"}, "minimumScore": 85,
	}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var report mutation.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Engine != "stryker-js" || executor.received.MinimumScore != 85 || executor.received.Paths[0] != "src/work.ts" {
		t.Fatalf("unexpected report/options: %#v %#v", report, executor.received)
	}
	if err := clientSession.Close(); err != nil {
		t.Fatal(err)
	}
	if err := serverSession.Wait(); err != nil {
		t.Fatal(err)
	}
}
