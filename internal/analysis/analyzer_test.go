package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzerFindsCSharpCallablesAndCountsBranches(t *testing.T) {
	root := t.TempDir()
	source := `namespace Demo;
public class Calculator
{
    public int Classify(int value)
    {
        int Local(int x) { return x > 0 ? x : -x; }
        if (value < 0) return -1;
        for (var i = 0; i < value; i++)
        {
            if (value > i && i > 0) return Local(i);
        }
        return value > 10 ? 1 : 2;
    }

    public int Value
    {
        get { if (true) return 1; return 0; }
        set { }
    }
}`
	path := filepath.Join(root, "Calculator.cs")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Methods) != 4 {
		t.Fatalf("got %d callables, want 4: %#v", len(report.Methods), report.Methods)
	}
	wantComplexity := map[string]int{
		"Demo.Calculator.Classify":  6,
		"Demo.Calculator.Local":     2,
		"Demo.Calculator.Value.get": 2,
		"Demo.Calculator.Value.set": 1,
	}
	for _, method := range report.Methods {
		if method.Language != "csharp" {
			t.Errorf("language = %q, want csharp", method.Language)
		}
		want, ok := wantComplexity[method.Name]
		if !ok {
			t.Errorf("unexpected callable %q", method.Name)
			continue
		}
		if method.Complexity != want {
			t.Errorf("%s complexity = %d, want %d", method.Name, method.Complexity, want)
		}
		if method.CoveragePercent != nil {
			t.Errorf("%s coverage should be unknown", method.Name)
		}
	}
}

func TestAnalyzerCountsGoBranchesAndMethods(t *testing.T) {
	root := t.TempDir()
	source := `package sample

func Classify(value int, ready bool) int {
	if value < 0 { return -1 }
	for i := 0; i < value; i++ { value-- }
	switch value {
	case 1, 2: return 1
	case 3: return 2
	default: return 0
	}
	if ready && value > 1 || value < -10 { return 3 }
	return 4
}

type Service struct{}
func (s *Service) Run(ready bool) { if ready { return } }
`
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"sample.Classify":       8,
		"sample.(*Service).Run": 2,
	}
	if len(report.Methods) != len(want) {
		t.Fatalf("got %d Go callables, want %d: %#v", len(report.Methods), len(want), report.Methods)
	}
	for _, method := range report.Methods {
		if method.Language != "go" {
			t.Errorf("language = %q, want go", method.Language)
		}
		if method.Complexity != want[method.Name] {
			t.Errorf("%s complexity = %d, want %d", method.Name, method.Complexity, want[method.Name])
		}
	}
}

func TestAnalyzerCountsTypeScriptCallablesAndBranches(t *testing.T) {
	root := t.TempDir()
	source := `namespace Demo {
export function classify(value: number, ready: boolean): number {
    if (value < 0) return -1;
    for (let i = 0; i < value; i++) value--;
    switch (value) {
    case 1: return 1;
    case 2: return 2;
    default: return ready && value > 2 || value < -10 ? 3 : 4;
    }
}

export function* values(): Generator<number> { yield 1; }

export class Worker {
    run(value: number): void {
        try { if (value > 0) return; } catch { return; }
    }
}

export const choose = (value: number): number => value > 0 ? value : -value;
}`
	if err := os.WriteFile(filepath.Join(root, "sample.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"Demo.classify":   8,
		"Demo.values":     1,
		"Demo.Worker.run": 3,
		"Demo.choose":     2,
	}
	if len(report.Methods) != len(want) {
		t.Fatalf("got %d TypeScript callables, want %d: %#v", len(report.Methods), len(want), report.Methods)
	}
	for _, method := range report.Methods {
		if method.Language != "typescript" {
			t.Errorf("language = %q, want typescript", method.Language)
		}
		if method.Complexity != want[method.Name] {
			t.Errorf("%s complexity = %d, want %d", method.Name, method.Complexity, want[method.Name])
		}
	}
}

func TestAnalyzerAppliesCoberturaToTypeScript(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	source := `export function work(ready: boolean): number {
    if (ready) return 1;
    return 0;
}`
	if err := os.WriteFile(filepath.Join(root, "src", "work.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage := `<coverage><packages><package><classes><class filename="src/work.ts"><lines>
<line number="2" hits="1"/><line number="3" hits="0"/>
</lines></class></classes></package></packages></coverage>`
	if err := os.WriteFile(filepath.Join(root, "coverage.xml"), []byte(coverage), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(Options{
		Root: root, Paths: []string{"src"}, CoveragePath: "coverage.xml", CRAPThreshold: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Methods) != 1 || report.Methods[0].CoveragePercent == nil {
		t.Fatalf("unexpected TypeScript coverage result: %#v", report.Methods)
	}
	method := report.Methods[0]
	if *method.CoveragePercent != 50 || method.CRAP != 2.5 {
		t.Fatalf("coverage = %v, CRAP = %.2f; want 50%% and 2.50", *method.CoveragePercent, method.CRAP)
	}
}

func TestTypeScriptCoverageUsesASTCallableOwnership(t *testing.T) {
	root := t.TempDir()
	source := `export function outer(flag: boolean): number {
  const inner = () => {
    return 1;
  };
  if (flag) return 2;
  return 0;
}`
	if err := os.WriteFile(filepath.Join(root, "work.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage := `<coverage><packages><package><classes><class filename="work.ts"><methods>` +
		`<method name="instrumented_outer"/><method name="instrumented_inner"/></methods><lines>` +
		`<line number="3" hits="1"/><line number="5" hits="0"/><line number="6" hits="1"/>` +
		`</lines></class></classes></package></packages></coverage>`
	if err := os.WriteFile(filepath.Join(root, "coverage.xml"), []byte(coverage), 0o600); err != nil {
		t.Fatal(err)
	}
	report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{"work.ts"}, CoveragePath: "coverage.xml", CRAPThreshold: 30})
	got := map[string]float64{}
	for _, method := range report.Methods {
		if method.CoveragePercent != nil {
			got[method.Name] = *method.CoveragePercent
		}
	}
	if got["outer"] != 50 || got["inner"] != 100 {
		t.Fatalf("callable coverage = %#v, want outer=50 inner=100", got)
	}
}

func TestCSharpCoverageExcludesLocalFunctions(t *testing.T) {
	root := t.TempDir()
	source := `class Worker {
  int Outer(bool flag) {
    int Inner() {
      return 1;
    }
    if (flag) return 2;
    return 0;
  }
}`
	if err := os.WriteFile(filepath.Join(root, "Worker.cs"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage := `<coverage><packages><package><classes><class filename="Worker.cs"><lines>` +
		`<line number="4" hits="1"/><line number="6" hits="0"/><line number="7" hits="1"/>` +
		`</lines></class></classes></package></packages></coverage>`
	if err := os.WriteFile(filepath.Join(root, "coverage.xml"), []byte(coverage), 0o600); err != nil {
		t.Fatal(err)
	}
	report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{"Worker.cs"}, CoveragePath: "coverage.xml", CRAPThreshold: 30})
	got := map[string]float64{}
	for _, method := range report.Methods {
		if method.CoveragePercent != nil {
			got[method.Name] = *method.CoveragePercent
		}
	}
	if got["Worker.Outer"] != 50 || got["Worker.Inner"] != 100 {
		t.Fatalf("callable coverage = %#v", got)
	}
}

func TestStrictCoverageRejectsUnmatchedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work.go"), []byte("package work\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage := `<coverage><packages><package><classes><class filename="other.go"><lines><line number="2" hits="1"/></lines></class></classes></package></packages></coverage>`
	if err := os.WriteFile(filepath.Join(root, "coverage.xml"), []byte(coverage), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	if _, err := analyzer.Analyze(Options{Root: root, CoveragePath: "coverage.xml", StrictCoverage: true}); err == nil {
		t.Fatal("strict coverage accepted an unmatched source path")
	}
}

func TestSameLineCallablesHaveOneCoverageOwner(t *testing.T) {
	root := t.TempDir()
	source := `const handlers = [() => 1, () => 2];`
	if err := os.WriteFile(filepath.Join(root, "work.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage := `<coverage><packages><package><classes><class filename="work.ts"><lines><line number="1" hits="1"/></lines></class></classes></package></packages></coverage>`
	if err := os.WriteFile(filepath.Join(root, "coverage.xml"), []byte(coverage), 0o600); err != nil {
		t.Fatal(err)
	}
	report := analyzeTestProject(t, root, Options{Root: root, CoveragePath: "coverage.xml", CRAPThreshold: 30})
	if len(report.Methods) != 2 || report.Methods[0].CoveragePercent == nil || *report.Methods[0].CoveragePercent != 100 || report.Methods[1].CoveragePercent != nil {
		t.Fatalf("same-line callable coverage = %#v", report.Methods)
	}
	if report.Methods[0].Name == report.Methods[1].Name {
		t.Fatalf("same-line callable names collided: %q", report.Methods[0].Name)
	}
}

func TestTypeScriptCallbackDoesNotInheritAssignmentName(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work.ts"), []byte(`const result = [1].map(value => value + 1);`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := analyzeTestProject(t, root, Options{Root: root, CRAPThreshold: 30})
	if len(report.Methods) != 1 || report.Methods[0].Name == "result" || !strings.HasPrefix(report.Methods[0].Name, "<anonymous@") {
		t.Fatalf("callback name = %#v", report.Methods)
	}
}

func TestTypeScriptNonNullWrappedCallableKeepsAssignmentName(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work.ts"), []byte(`const handler = (() => {})!;`), 0o600); err != nil {
		t.Fatal(err)
	}
	report := analyzeTestProject(t, root, Options{Root: root, CRAPThreshold: 30})
	if len(report.Methods) != 1 || report.Methods[0].Name != "handler" {
		t.Fatalf("wrapped callable name = %#v", report.Methods)
	}
}

func analyzeTestProject(t *testing.T, root string, options Options) Report {
	t.Helper()
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(options)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestAnalyzerParsesTSXAndNamesComponents(t *testing.T) {
	root := t.TempDir()
	source := `type Props = { ready: boolean; count: number };
export const Status = ({ ready, count }: Props) => (
    <section>{ready && (count > 0 ? <b>Ready</b> : <i>Empty</i>)}</section>
);`
	if err := os.WriteFile(filepath.Join(root, "Status.tsx"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	report, err := analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Methods) != 1 {
		t.Fatalf("got %d TSX callables, want 1: %#v", len(report.Methods), report.Methods)
	}
	method := report.Methods[0]
	if method.Name != "Status" || method.Language != "typescript" || method.Complexity != 3 {
		t.Fatalf("TSX result = %#v, want Status with complexity 3", method)
	}
}

func TestAnalyzerParsesModernCSharp13Syntax(t *testing.T) {
	root := t.TempDir()
	source := `namespace Demo;
public record Person(string Name)
{
    public required int Age { get; init; }
    public string Describe(int[] values)
    {
        var text = $"""Name: {{Name}}""";
        return values is [1, ..] and not [] ? text : "none";
    }
}`
	if err := os.WriteFile(filepath.Join(root, "Modern.cs"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	if _, err := analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30}); err != nil {
		t.Fatalf("parse modern C# syntax: %v", err)
	}
}
