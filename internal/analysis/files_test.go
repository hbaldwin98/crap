package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindSourceFilesExcludesGoTestsByDefault(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"main.go", "main_test.go", "Example.cs", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := findSourceFiles(root, []string{"."}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("default files = %v, want main.go and Example.cs", files)
	}
	files, err = findSourceFiles(root, []string{"."}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files with tests = %v, want three source files", files)
	}
}
