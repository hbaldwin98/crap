package analysis

import (
	"path/filepath"
	"strings"
	"testing"
)

func callGraphFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "callgraph"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	return root
}

func callGraphFunctionBySuffix(report CallGraphReport, suffix string) (CallGraphFunction, bool) {
	for _, function := range report.Functions {
		if strings.HasSuffix(function.Name, suffix) {
			return function, true
		}
	}
	return CallGraphFunction{}, false
}

func callGraphHasEdge(report CallGraphReport, from, to, kind string) bool {
	bySuffix := make(map[string]string, len(report.Functions))
	for _, function := range report.Functions {
		bySuffix[function.ID] = function.Name
	}
	for _, edge := range report.Edges {
		if edge.Kind != kind {
			continue
		}
		if strings.HasSuffix(bySuffix[edge.From], from) && strings.HasSuffix(bySuffix[edge.To], to) {
			return true
		}
	}
	return false
}

func callGraphUnresolvedReasons(report CallGraphReport) map[string]int {
	reasons := make(map[string]int)
	for _, call := range report.UnresolvedCalls {
		reasons[call.Reason]++
	}
	return reasons
}

func TestAnalyzeCallGraphReportsModuleEdges(t *testing.T) {
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatalf("new analyzer: %v", err)
	}
	defer analyzer.Close()
	report, err := analyzer.AnalyzeCallGraph(CallGraphOptions{Root: callGraphFixture(t)})
	if err != nil {
		t.Fatalf("analyze call graph: %v", err)
	}
	if report.ReportType != "call-graph" || report.SchemaVersion != CallGraphSchemaVersion {
		t.Fatalf("unexpected report identity %+v", report.ReportType)
	}
	if report.Module.Path != "example.com/callgraph" {
		t.Fatalf("unexpected module path %q", report.Module.Path)
	}
	if report.Module.SHA256 == "" || report.Compiler.Version == "" {
		t.Fatalf("module fingerprint or compiler version missing: %+v", report.Module)
	}
	if report.Summary.Packages == 0 || report.Summary.Functions == 0 || report.Summary.Edges == 0 {
		t.Fatalf("empty summary: %+v", report.Summary)
	}
	if report.Summary.UnmatchedCallables != 0 {
		t.Fatalf("expected every fixture callable to match, got %d unmatched", report.Summary.UnmatchedCallables)
	}
	for _, suffix := range []string{".prefix", ".GreetWith", ".FormalGreeting", ".total", ".doubles", ".shout", ".(Formal).Greet", "(*Casual).Greet", ".each"} {
		if _, ok := callGraphFunctionBySuffix(report, suffix); !ok {
			t.Fatalf("missing function %q in call graph", suffix)
		}
	}
	tests := 0
	for _, suffix := range []string{"TestFormalGreeting", "TestShout"} {
		function, ok := callGraphFunctionBySuffix(report, suffix)
		if !ok {
			t.Fatalf("missing test function %q", suffix)
		}
		if !function.Test {
			t.Fatalf("expected %q to be a test function", suffix)
		}
		tests++
	}
	if function, ok := callGraphFunctionBySuffix(report, "helperTotals"); ok && function.Test {
		t.Fatalf("helperTotals must not be a test function")
	}
	if report.Summary.Tests != tests {
		t.Fatalf("expected %d tests, got %d", tests, report.Summary.Tests)
	}
	staticEdges := []struct{ from, to string }{
		{"TestFormalGreeting", ".FormalGreeting"},
		{".FormalGreeting", ".(Formal).Greet"},
		{".(Formal).Greet", ".prefix"},
		{"TestShout", ".shout"},
		{".shout", ".GreetWith"},
		{"helperTotals", ".total"},
	}
	for _, expected := range staticEdges {
		if !callGraphHasEdge(report, expected.from, expected.to, "static") {
			t.Fatalf("missing static edge %s -> %s", expected.from, expected.to)
		}
	}
	if !callGraphHasEdge(report, ".GreetWith", ".(Formal).Greet", "dispatch") {
		t.Fatalf("missing dispatch edge GreetWith -> Formal.Greet")
	}
	if !callGraphHasEdge(report, ".GreetWith", "(*Casual).Greet", "dispatch") {
		t.Fatalf("missing dispatch edge GreetWith -> Casual.Greet")
	}
	reasons := callGraphUnresolvedReasons(report)
	if reasons["outside-module"] == 0 {
		t.Fatalf("expected outside-module unresolved calls for strings.ToUpper, got %v", reasons)
	}
	if report.Summary.StaticEdges+report.Summary.DispatchEdges != report.Summary.Edges {
		t.Fatalf("edge kind counts must sum to edges: %+v", report.Summary)
	}
	if report.Summary.AffectedTests != 0 || len(report.AffectedTests) != 0 {
		t.Fatalf("affected tests require a diff base: %+v", report.Summary)
	}
}

func TestAnalyzeCallGraphRequiresGoModule(t *testing.T) {
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatalf("new analyzer: %v", err)
	}
	defer analyzer.Close()
	_, err = analyzer.AnalyzeCallGraph(CallGraphOptions{Root: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("expected go.mod error, got %v", err)
	}
}

func TestBuildAffectedTestsTraversesReverseEdges(t *testing.T) {
	changed := CallGraphFunction{ID: "changed", Name: "lib.changed", File: "lib.go", Changed: true}
	caller := CallGraphFunction{ID: "caller", Name: "lib.caller", File: "lib.go"}
	test := CallGraphFunction{ID: "test", Name: "lib.TestDirect", File: "lib_test.go", Test: true}
	indirect := CallGraphFunction{ID: "indirect", Name: "lib.TestIndirect", File: "lib_test.go", Test: true}
	isolated := CallGraphFunction{ID: "isolated", Name: "lib.TestIsolated", File: "lib_test.go", Test: true}
	functions := []CallGraphFunction{changed, caller, test, indirect, isolated}
	edges := []CallGraphEdge{
		{Kind: "static", From: "caller", To: "changed"},
		{Kind: "static", From: "test", To: "caller"},
		{Kind: "static", From: "indirect", To: "changed"},
	}
	affected := buildAffectedTests(edges, functions)
	if len(affected) != 2 {
		t.Fatalf("expected 2 affected tests, got %d: %+v", len(affected), affected)
	}
	byName := make(map[string]CallGraphAffectedTest, len(affected))
	for _, entry := range affected {
		byName[entry.Name] = entry
	}
	direct, ok := byName["lib.TestDirect"]
	if !ok {
		t.Fatalf("missing TestDirect: %+v", affected)
	}
	if direct.Distance != 2 {
		t.Fatalf("expected distance 2 for TestDirect, got %d", direct.Distance)
	}
	if len(direct.Seeds) != 1 || direct.Seeds[0] != "changed" {
		t.Fatalf("unexpected seeds for TestDirect: %+v", direct.Seeds)
	}
	indirectEntry, ok := byName["lib.TestIndirect"]
	if !ok {
		t.Fatalf("missing TestIndirect: %+v", affected)
	}
	if indirectEntry.Distance != 1 {
		t.Fatalf("expected distance 1 for TestIndirect, got %d", indirectEntry.Distance)
	}
	if _, ok := byName["lib.TestIsolated"]; ok {
		t.Fatalf("isolated test must not be affected")
	}
}

func TestCallGraphModuleRootResolvesFromSubdirectory(t *testing.T) {
	root, err := callGraphModuleRoot("", []string{filepath.Join(callGraphFixture(t), "library")})
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if root != callGraphFixture(t) {
		t.Fatalf("expected module root %s, got %s", callGraphFixture(t), root)
	}
}
