package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hbaldwin98/crap/internal/analysis"
	"github.com/hbaldwin98/crap/internal/mutation"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var contractFixtures = map[string]string{
	"analysis-report-v4.schema.json":   "analysis-report-v4.json",
	"mutation-report-v3.schema.json":   "mutation-report-v3.json",
	"analysis-mcp-page-v1.schema.json": "analysis-mcp-page-v1.json",
	"mutation-mcp-page-v2.schema.json": "mutation-mcp-page-v2.json",
	"mutation-plan-v2.schema.json":     "mutation-plan-v2.json",
	"mutation-doctor-v1.schema.json":   "mutation-doctor-v1.json",
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
	validateValue(t, filepath.Join(root, "schemas", "v1", "analysis-report-v4.schema.json"), report)

	plan, err := mutation.NewService().Plan(mutation.Options{Root: project, Language: "go", Paths: []string{"."}, MinimumScore: 80, TimeoutSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	validateValue(t, filepath.Join(root, "schemas", "v1", "mutation-plan-v2.schema.json"), plan)
}

func TestSchemasRejectInvalidContracts(t *testing.T) {
	root := repositoryRoot(t)
	schema := compileSchema(t, filepath.Join(root, "schemas", "v1", "analysis-report-v4.schema.json"))
	fixture := filepath.Join(root, "testdata", "contracts", "analysis-report-v4.json")
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
