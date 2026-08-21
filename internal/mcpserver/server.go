package mcpserver

import (
	"context"
	"fmt"
	"os"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type AnalyzeInput struct {
	Root          string   `json:"root,omitempty" jsonschema:"Working directory used to resolve paths, coverage, and Git revisions. Defaults to the MCP server working directory."`
	Paths         []string `json:"paths,omitempty" jsonschema:"C# or Go files and directories to analyze, relative to root. Defaults to the root directory."`
	CoveragePath  string   `json:"coveragePath,omitempty" jsonschema:"Cobertura XML or Go coverprofile path, relative to root. Omit to score with unknown coverage treated as zero."`
	DiffBase      string   `json:"diffBase,omitempty" jsonschema:"Git revision to compare against. When set, return only callables intersecting added or modified lines."`
	CRAPThreshold *float64 `json:"crapThreshold,omitempty" jsonschema:"Score above which a callable is flagged. Defaults to 30."`
}

func Run(ctx context.Context, version string) error {
	return New(version).Run(ctx, &mcp.StdioTransport{})
}

func New(version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "crap", Version: version}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze_code",
		Description: "Deterministically calculate cyclomatic complexity, coverage, and CRAP scores for C# and Go callables. Can restrict results to callables changed from a Git revision.",
	}, analyze)
	return server
}

func analyze(_ context.Context, _ *mcp.CallToolRequest, input AnalyzeInput) (*mcp.CallToolResult, analysis.Report, error) {
	root := input.Root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return nil, analysis.Report{}, err
		}
	}
	threshold := 30.0
	if input.CRAPThreshold != nil {
		threshold = *input.CRAPThreshold
	}
	if threshold < 0 {
		return nil, analysis.Report{}, fmt.Errorf("crapThreshold must not be negative")
	}
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		return nil, analysis.Report{}, err
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(analysis.Options{
		Root:          root,
		Paths:         input.Paths,
		CoveragePath:  input.CoveragePath,
		DiffBase:      input.DiffBase,
		CRAPThreshold: threshold,
	})
	return nil, report, err
}
