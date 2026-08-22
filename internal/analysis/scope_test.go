package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeChangeScopeMapsGitRangesToCallableSeeds(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "work.go")
	baseline := "package work\n\nfunc Changed() int {\n\treturn 1\n}\n\nfunc Unchanged() int {\n\treturn 2\n}\n"
	if err := os.WriteFile(path, []byte(baseline), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	changed := "package work\n\nfunc Changed() int {\n\treturn 3\n}\n\nfunc Unchanged() int {\n\treturn 2\n}\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}

	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	options := Options{Root: root, Paths: []string{"."}, DiffBase: "HEAD", CRAPThreshold: 17}
	first, err := analyzer.AnalyzeChangeScope(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := analyzer.AnalyzeChangeScope(options)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReportType != "change-scope" || first.SchemaVersion != "1" || first.Mode != "actual" || first.Threshold != 17 {
		t.Fatalf("unexpected contract: %#v", first)
	}
	if len(first.Files) != 1 || first.Files[0].Path != "work.go" || len(first.Files[0].Ranges) != 1 || first.Files[0].Ranges[0] != (ChangeScopeRange{StartLine: 4, EndLine: 4}) {
		t.Fatalf("changed files = %#v", first.Files)
	}
	if len(first.Callables) != 1 || first.Callables[0].Name != "work.Changed" || !first.Callables[0].Changed {
		t.Fatalf("callables = %#v", first.Callables)
	}
	if len(first.Edges) != 1 || first.Edges[0].Type != "contains" || first.Edges[0].From != first.Files[0].ID || first.Edges[0].To != first.Callables[0].ID {
		t.Fatalf("edges = %#v", first.Edges)
	}
	if len(first.Seeds) != 2 || first.Summary.ChangedFiles != 1 || first.Summary.ChangedCallables != 1 || first.Summary.Truncated {
		t.Fatalf("seeds or summary = %#v %#v", first.Seeds, first.Summary)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if string(left) != string(right) {
		t.Fatal("change scope output is not deterministic")
	}
}

func TestAnalyzeChangeScopeRequiresDiffBase(t *testing.T) {
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	if _, err := analyzer.AnalyzeChangeScope(Options{Root: t.TempDir()}); err == nil {
		t.Fatal("missing diff base was accepted")
	}
}
