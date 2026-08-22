package contracts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/mcpserver"
	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var contractFixtures = map[string]string{
	"analysis-report-v4.schema.json":             "analysis-report-v4.json",
	"analysis-report-v5.schema.json":             "analysis-report-v5.json",
	"analysis-report-v6.schema.json":             "analysis-report-v6.json",
	"mutation-report-v3.schema.json":             "mutation-report-v3.json",
	"analysis-mcp-page-v1.schema.json":           "analysis-mcp-page-v1.json",
	"analysis-mcp-page-v2.schema.json":           "analysis-mcp-page-v2.json",
	"analysis-mcp-page-v3.schema.json":           "analysis-mcp-page-v3.json",
	"analysis-mcp-page-v4.schema.json":           "analysis-mcp-page-v4.json",
	"mutation-mcp-page-v2.schema.json":           "mutation-mcp-page-v2.json",
	"mutation-plan-v2.schema.json":               "mutation-plan-v2.json",
	"mutation-doctor-v1.schema.json":             "mutation-doctor-v1.json",
	"change-scope-v1.schema.json":                "change-scope-v1.json",
	"change-scope-mcp-v1.schema.json":            "change-scope-mcp-v1.json",
	"change-scope-comparison-v1.schema.json":     "change-scope-comparison-v1.json",
	"change-scope-comparison-mcp-v1.schema.json": "change-scope-comparison-mcp-v1.json",
	"code-graph-v1.schema.json":                  "code-graph-v1.json",
	"code-graph-mcp-page-v1.schema.json":         "code-graph-mcp-page-v1.json",
	"code-graph-neighborhood-mcp-v1.schema.json": "code-graph-neighborhood-mcp-v1.json",
}

func TestGoldenContractFixtures(t *testing.T) {
	root := repositoryRoot(t)
	for schemaFile, fixtureFile := range contractFixtures {
		t.Run(fixtureFile, func(t *testing.T) {
			validateFile(t, filepath.Join(root, "schemas", "v1", schemaFile), filepath.Join(root, "testdata", "contracts", fixtureFile))
		})
	}
}

func TestGeneratedAnalysisAndPlanValidate(t *testing.T) {
	root := repositoryRoot(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "work.go"), []byte("package work\n\nfunc Run() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := analysis.NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(analysis.Options{Root: project, Paths: []string{"work.go"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "analysis-report-v6.schema.json"), report)
	runGit(t, project, "init")
	runGit(t, project, "add", ".")
	runGit(t, project, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	if err := os.WriteFile(filepath.Join(project, "work.go"), []byte("package work\n\nfunc Run() { println(1) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := analyzer.AnalyzeChangeScope(analysis.Options{Root: project, Paths: []string{"work.go"}, DiffBase: "HEAD", CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "change-scope-v1.schema.json"), scope)
	validateValue(t, filepath.Join(root, "schemas", "v1", "change-scope-mcp-v1.schema.json"), mcpserver.ChangeScopeOutput{
		PageSchemaVersion: "1",
		ReportID:          strings.Repeat("1", 32),
		ExpiresAt:         time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Report:            scope,
	})
	comparison, err := analyzer.CompareChangeScope(analysis.ComparisonOptions{BaseRevision: "HEAD", Analysis: analysis.Options{Root: project, Paths: []string{"work.go"}, CRAPThreshold: 30}})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "change-scope-comparison-v1.schema.json"), comparison)
	validateValue(t, filepath.Join(root, "schemas", "v1", "change-scope-comparison-mcp-v1.schema.json"), mcpserver.ChangeScopeComparisonOutput{
		PageSchemaVersion: "1",
		ReportID:          strings.Repeat("2", 32),
		ExpiresAt:         time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Report:            comparison,
	})
	graph, err := analyzer.AnalyzeCodeGraph(analysis.CodeGraphOptions{Root: project, Paths: []string{"work.go"}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "code-graph-v1.schema.json"), graph)
	expiresAt := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	validateValue(t, filepath.Join(root, "schemas", "v1", "code-graph-mcp-page-v1.schema.json"), mcpserver.CodeGraphOutput{
		PageSchemaVersion: "1", ReportType: "code-graph-page", ReportID: strings.Repeat("3", 32), ExpiresAt: expiresAt,
		SchemaVersion: graph.SchemaVersion, Tool: graph.Tool, Fingerprints: graph.Fingerprints, Coordinates: graph.Coordinates,
		Grammars: graph.Grammars, Coverage: graph.Coverage, Discovery: graph.Discovery, Threshold: graph.Threshold,
		Policy: graph.Policy, Summary: graph.Summary,
		Page:  mcpserver.Page{ResultMode: "nodes", TotalMatched: len(graph.Nodes), Limit: 100, Returned: len(graph.Nodes)},
		Nodes: graph.Nodes, Edges: []analysis.CodeGraphEdge{}, References: []analysis.CodeGraphReference{}, ResolutionInputs: graph.ResolutionInputs, Limitations: graph.Limitations, Diagnostics: graph.Diagnostics,
	})
	neighborhood, err := analysis.BuildCodeGraphNeighborhood(graph, analysis.CodeGraphNeighborhoodOptions{SeedNodeIDs: []string{graph.Nodes[0].ID}, Depth: 0, MaximumNodes: 100, MaximumEdges: 200})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "code-graph-neighborhood-mcp-v1.schema.json"), mcpserver.CodeGraphNeighborhoodOutput{
		PageSchemaVersion: "1", ReportID: strings.Repeat("3", 32), ExpiresAt: expiresAt, Neighborhood: neighborhood,
	})

	plan, err := mutation.NewService().Plan(mutation.Options{Root: project, Language: "go", Paths: []string{"."}, MinimumScore: 80, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "mutation-plan-v2.schema.json"), plan)
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func TestSchemasRejectInvalidContracts(t *testing.T) {
	root := repositoryRoot(t)
	schema := compileSchema(t, filepath.Join(root, "schemas", "v1", "analysis-report-v6.schema.json"))
	fixture := filepath.Join(root, "testdata", "contracts", "analysis-report-v6.json")
	tests := map[string]func(map[string]any){
		"version":          func(value map[string]any) { value["schemaVersion"] = "3" },
		"unknown property": func(value map[string]any) { value["timestamp"] = "2026-01-01T00:00:00Z" },
		"digest":           func(value map[string]any) { value["fingerprints"].(map[string]any)["configSha256"] = "bad" },
		"coordinate bound": func(value map[string]any) { value["methods"].([]any)[0].(map[string]any)["startColumn"] = float64(0) },
		"embedded traversal": func(value map[string]any) {
			value["methods"].([]any)[0].(map[string]any)["file"] = "src/../../secret.go"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := decodeFile(t, fixture)
			mutate(value)
			if err := schema.Validate(value); err == nil {
				t.Fatal("invalid contract passed schema validation")
			}
		})
	}
}

func validateFile(t *testing.T, schemaPath, fixturePath string) {
	t.Helper()
	if err := compileSchema(t, schemaPath).Validate(decodeFile(t, fixturePath)); err != nil {
		t.Fatalf("validate %s: %v", fixturePath, err)
	}
}

func validateValue(t *testing.T, schemaPath string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := compileSchema(t, schemaPath).Validate(decoded); err != nil {
		t.Fatalf("validate generated output: %v\n%s", err, data)
	}
}

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	schema, err := compiler.Compile(path)
	if err != nil {
		t.Fatalf("compile %s: %v", path, err)
	}
	return schema
}

func decodeFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
