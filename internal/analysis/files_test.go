package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSourceFilesExcludesGoTestsByDefault(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"main.go", "main_test.go", "Example.cs", "app.ts", "app.spec.ts", "view.tsx", "view.test.tsx", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := findSourceFiles(root, []string{"."}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("default files = %v, want Go, C#, TypeScript, and TSX source", files)
	}
	files, err = findSourceFiles(root, []string{"."}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 7 {
		t.Fatalf("files with tests = %v, want seven source files", files)
	}
}
