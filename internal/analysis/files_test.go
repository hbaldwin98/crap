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

func TestFindSourceFilesRejectsSourcesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findSourceFiles(root, []string{outside}, false, nil); err == nil {
		t.Fatal("outside source was accepted")
	}
}

func TestFindSourceFilesRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := findSourceFiles(root, []string{"link.go"}, false, nil); err == nil {
		t.Fatal("outside source symlink was accepted")
	}
}
