package analysis

import (
	"os"
	"path/filepath"
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
