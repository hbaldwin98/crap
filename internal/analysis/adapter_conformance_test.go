package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLanguageAdapterConformance(t *testing.T) {
	type expectedCallable struct {
		name       string
		kind       string
		start, end int
		complexity int
	}
	tests := []struct {
		file string
		want []expectedCallable
	}{
		{
			file: "callables.cs",
			want: []expectedCallable{
				{"Conformance.Callables.Score", "property", 5, 5, 2},
				{"Conformance.Callables.this", "indexer", 6, 6, 3},
				{"Conformance.Callables.Initialized.get", "accessor", 7, 7, 1},
				{"Conformance.Callables.Outer", "method", 9, 21, 2},
				{"Conformance.Callables.choose", "lambda", 11, 14, 2},
				{"Conformance.Callables.callback", "anonymous_method", 15, 18, 2},
				{"Conformance.Callables.Register", "method", 23, 26, 1},
				{"Conformance.Callables.<anonymous@25:13>", "lambda", 25, 25, 2},
			},
		},
		{
			file: "callables.go",
			want: []expectedCallable{
				{"conformance.Outer", "function", 3, 14, 2},
				{"conformance.handler", "function_literal", 4, 9, 3},
				{"conformance.PackageHandler", "function_literal", 16, 20, 2},
				{"conformance.Register", "function", 22, 26, 1},
				{"conformance.<anonymous@23:6>", "function_literal", 23, 25, 2},
			},
		},
		{
			file: "callables.rs",
			want: []expectedCallable{
				{"Worker::outer", "function", 4, 15, 2},
				{"Worker::handler", "closure", 5, 10, 3},
				{"registry::register", "function", 19, 21, 1},
				{"registry::<anonymous@20:30>", "closure", 20, 20, 2},
			},
		},
		{
			file: "callables.ts",
			want: []expectedCallable{
				{"Conformance.outer", "function", 2, 6, 2},
				{"Conformance.nested", "arrow_function", 3, 3, 3},
				{"Conformance.Worker.run", "method", 9, 15, 3},
			},
		},
		{
			file: "branches.cs",
			want: []expectedCallable{
				{"Branches.Count", "method", 3, 18, 15},
			},
		},
		{
			file: "branches.go",
			want: []expectedCallable{
				{"conformance.Count", "function", 3, 27, 10},
			},
		},
		{
			file: "branches.rs",
			want: []expectedCallable{
				{"count", "function", 1, 23, 11},
			},
		},
		{
			file: "branches.ts",
			want: []expectedCallable{
				{"count", "function", 1, 12, 13},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			root, err := filepath.Abs("testdata")
			if err != nil {
				t.Fatal(err)
			}
			report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{test.file}, CRAPThreshold: 30})
			if len(report.Methods) != len(test.want) {
				t.Fatalf("got %d callables, want %d: %#v", len(report.Methods), len(test.want), report.Methods)
			}
			for index, want := range test.want {
				got := report.Methods[index]
				if got.Name != want.name || got.Kind != want.kind || got.StartLine != want.start || got.EndLine != want.end || got.Complexity != want.complexity {
					t.Errorf("callable %d = {%q %q %d-%d complexity %d}, want {%q %q %d-%d complexity %d}", index, got.Name, got.Kind, got.StartLine, got.EndLine, got.Complexity, want.name, want.kind, want.start, want.end, want.complexity)
				}
				if got.Signature == "" || len(got.ID) != 64 {
					t.Errorf("callable %q has invalid contract metadata: signature=%q id=%q", got.Name, got.Signature, got.ID)
				}
			}
		})
	}
}

func TestAddedCallableIDsIgnoreBodyAndLeadingLines(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		before    string
		after     string
		kind      string
		nameMatch string
	}{
		{
			name: "Go assigned function literal", file: "work.go", kind: "function_literal", nameMatch: "stable",
			before: "package sample\nvar stable = func(value int) int { return value + 1 }\n",
			after:  "package sample\n\nvar stable = func(value int) int { if value > 0 { return value }; return 2 }\n",
		},
		{
			name: "Go anonymous function literal", file: "work.go", kind: "function_literal", nameMatch: "<anonymous@",
			before: "package sample\nfunc outer() { use(func(value int) int { return value + 1 }) }\n",
			after:  "package sample\n\nfunc outer() { use(func(value int) int { if value > 0 { return value }; return 2 }) }\n",
		},
		{
			name: "C# assigned lambda", file: "Work.cs", kind: "lambda", nameMatch: "stable",
			before: "class Work { void Outer() { Func<int, int> stable = value => value + 1; } }\n",
			after:  "\nclass Work { void Outer() { Func<int, int> stable = value => value > 0 ? value : 2; } }\n",
		},
		{
			name: "C# anonymous method", file: "Work.cs", kind: "anonymous_method", nameMatch: "<anonymous@",
			before: "class Work { void Outer() { Use(delegate(int value) { return value + 1; }); } }\n",
			after:  "\nclass Work { void Outer() { Use(delegate(int value) { if (value > 0) return value; return 2; }); } }\n",
		},
		{
			name: "C# expression-bodied property", file: "Work.cs", kind: "property", nameMatch: "Stable",
			before: "class Work { int Stable => value + 1; }\n",
			after:  "\nclass Work { int Stable => value > 0 ? value : 2; }\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, test.file)
			analyze := func(source string) MethodResult {
				t.Helper()
				if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
				report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{test.file}, CRAPThreshold: 30})
				for _, method := range report.Methods {
					if method.Kind == test.kind && strings.Contains(method.Name, test.nameMatch) {
						return method
					}
				}
				t.Fatalf("missing %s %q in %#v", test.kind, test.nameMatch, report.Methods)
				return MethodResult{}
			}
			before, after := analyze(test.before), analyze(test.after)
			if before.ID != after.ID {
				t.Fatalf("ID changed with body/leading lines: %s != %s", before.ID, after.ID)
			}
			if before.StartLine == after.StartLine {
				t.Fatalf("fixture did not move callable: %#v != %#v", before, after)
			}
		})
	}
}

func TestAdapterNamesUseLanguageLevelIdentities(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		source   string
		wantName []string
	}{
		{
			name: "formatted generic Go receiver", file: "work.go",
			source:   "package sample\ntype Pair[A, B any] struct{}\nfunc (p *Pair[A, B]) Run() {}\n",
			wantName: []string{"sample.(*Pair[A,B]).Run"},
		},
		{
			name: "C# indexers and operators", file: "Work.cs",
			source: "class Work {\n" +
				"int this[int value] { get { return value; } }\n" +
				"string this[string value] { get { return value; } }\n" +
				"public static Work operator +(Work left, Work right) => left;\n" +
				"public static explicit operator int(Work value) => 1;\n" +
				"}\n",
			wantName: []string{"Work.this.get", "Work.this.get", "Work.operator +", "Work.explicit operator int"},
		},
		{
			name: "Rust trait, impl, and module scopes", file: "work.rs",
			source: `pub trait Store {
    fn put(&self) -> bool { true }
}
pub struct Memory;
impl Memory {
    pub fn load(&self) {}
}
impl Store for Memory {
    fn put(&self) -> bool { false }
}
mod inner {
    pub fn helper() {}
}
`,
			wantName: []string{"Store::put", "Memory::load", "<Memory as Store>::put", "inner::helper"},
		},
		{
			name: "TypeScript module", file: "work.ts",
			source:   "module Demo { export function run() {} }\n",
			wantName: []string{"Demo.run"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, test.file), []byte(test.source), 0o600); err != nil {
				t.Fatal(err)
			}
			report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{test.file}, CRAPThreshold: 30})
			if len(report.Methods) != len(test.wantName) {
				t.Fatalf("methods = %#v, want names %v", report.Methods, test.wantName)
			}
			for index, want := range test.wantName {
				if report.Methods[index].Name != want {
					t.Errorf("method %d name = %q, want %q", index, report.Methods[index].Name, want)
				}
			}
			if test.name == "C# indexers and operators" && report.Methods[0].ID == report.Methods[1].ID {
				t.Fatal("indexer accessor IDs collided")
			}
		})
	}
}

func TestNestedAnonymousCallableIDIgnoresLeadingLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work.ts")
	analyze := func(source string) MethodResult {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{"work.ts"}, CRAPThreshold: 30})
		for _, method := range report.Methods {
			if method.Kind == "arrow_function" && method.Name == "inner" {
				return method
			}
		}
		t.Fatalf("nested anonymous callable missing from %#v", report.Methods)
		return MethodResult{}
	}
	before := analyze("use(() => { const inner = () => 1; return inner(); });\n")
	after := analyze("\n\nuse(() => { const inner = () => 1; return inner(); });\n")
	if before.ID != after.ID {
		t.Fatalf("nested anonymous ID changed with leading lines: %s != %s", before.ID, after.ID)
	}
}

func TestCSharpExplicitInterfaceAndCheckedOperatorNames(t *testing.T) {
	root := t.TempDir()
	source := `interface IA { int Value { get; } }
interface IB { int Value { get; } }
class Work : IA, IB {
    int IA.Value { get { return 1; } }
    int IB.Value { get { return 2; } }
    public static Work operator checked +(Work left, Work right) => left;
    public static explicit operator checked int(Work value) => 1;
}`
	if err := os.WriteFile(filepath.Join(root, "Work.cs"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	report := analyzeTestProject(t, root, Options{Root: root, Paths: []string{"Work.cs"}, CRAPThreshold: 30})
	methods := make([]MethodResult, 0, 4)
	for _, method := range report.Methods {
		if strings.HasPrefix(method.Name, "Work.") {
			methods = append(methods, method)
		}
	}
	want := []string{"Work.IA.Value.get", "Work.IB.Value.get", "Work.checked operator +", "Work.checked explicit operator int"}
	if len(methods) != len(want) {
		t.Fatalf("methods = %#v, want names %v", methods, want)
	}
	for index, name := range want {
		if methods[index].Name != name {
			t.Errorf("method %d name = %q, want %q", index, methods[index].Name, name)
		}
	}
	if methods[0].ID == methods[1].ID {
		t.Fatal("explicit-interface accessor IDs collided")
	}
}
