package mutationmcp

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type RunInput struct {
	Root           string   `json:"root,omitempty" jsonschema:"Project root in which the mutation engine runs. Defaults to the MCP server working directory."`
	Language       string   `json:"language" jsonschema:"Required language: csharp, go, or typescript."`
	Paths          []string `json:"paths,omitempty" jsonschema:"Production source paths or globs to mutate. Go accepts one package directory; C# and TypeScript accept engine globs."`
	MinimumScore   *float64 `json:"minimumScore,omitempty" jsonschema:"Minimum accepted mutation score from 0 through 100. Defaults to 80."`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty" jsonschema:"Maximum engine runtime in seconds. Defaults to 1800."`
	Incremental    bool     `json:"incremental,omitempty" jsonschema:"Enable StrykerJS incremental mode. TypeScript only."`
	ReportPath     string   `json:"reportPath,omitempty" jsonschema:"StrykerJS JSON report path when project configuration changes its default location."`
}

type executor interface {
	Run(context.Context, mutation.Options, io.Writer) (mutation.Report, error)
}

func Run(ctx context.Context, version string) error {
	return New(version).Run(ctx, &mcp.StdioTransport{})
}

func New(version string) *mcp.Server {
	return newServer(version, mutation.NewService())
}

func newServer(version string, service executor) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "crap-mutate", Version: version}, &mcp.ServerOptions{
		Instructions: "Use run_mutation_tests when the user asks to run mutation testing for C#, Go, or TypeScript. The native engine must produce every score; never infer mutation scores yourself. Mutation runs execute project tests and can be slow, so confirm the project root and language from context before calling.",
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "run_mutation_tests",
		Description: "Run Stryker.NET, Gremlins, or StrykerJS and return a normalized, sorted mutation report. Scores are produced from engine output, not inferred by an AI caller.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input RunInput) (*mcp.CallToolResult, mutation.Report, error) {
		return runMutation(ctx, service, input)
	})
	return server
}

func runMutation(ctx context.Context, service executor, input RunInput) (*mcp.CallToolResult, mutation.Report, error) {
	root := input.Root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return nil, mutation.Report{}, err
		}
	}
	minimum := 80.0
	if input.MinimumScore != nil {
		minimum = *input.MinimumScore
	}
	if math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum < 0 || minimum > 100 {
		return nil, mutation.Report{}, fmt.Errorf("minimumScore must be between 0 and 100")
	}
	report, err := service.Run(ctx, mutation.Options{
		Root: root, Language: input.Language, Paths: input.Paths, MinimumScore: minimum,
		TimeoutSeconds: input.TimeoutSeconds, Incremental: input.Incremental, ReportPath: input.ReportPath,
	}, nil)
	return nil, report, err
}
