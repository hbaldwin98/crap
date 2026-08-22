package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteArchiveIsDeterministic(t *testing.T) {
	root := t.TempDir()
	for name, contents := range map[string]string{"crap": "analysis", "crap-mutate": "mutation"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, extension := range []string{".tar.gz", ".zip"} {
		first := filepath.Join(t.TempDir(), "first"+extension)
		second := filepath.Join(t.TempDir(), "second"+extension)
		if err := writeArchive(first, root, []string{"crap-mutate", "crap"}); err != nil {
			t.Fatal(err)
		}
		if err := writeArchive(second, root, []string{"crap", "crap-mutate"}); err != nil {
			t.Fatal(err)
		}
		firstBytes, err := os.ReadFile(first)
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := os.ReadFile(second)
		if err != nil {
			t.Fatal(err)
		}
		if string(firstBytes) != string(secondBytes) {
			t.Fatalf("%s archives differ", extension)
		}
	}
}

func TestRunRejectsVersionAndRevisionMismatch(t *testing.T) {
	if err := run("0.2.1", "e68097dfe68097dfe68097dfe68097dfe68097df", t.TempDir()); err == nil {
		t.Fatal("mismatched release version was accepted")
	}
	if err := run("0.2.0", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", t.TempDir()); err == nil {
		t.Fatal("non-hexadecimal revision was accepted")
	}
}

func TestStageLicensesIncludesProjectAndCompiledDependencies(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	work := t.TempDir()
	names, err := stageLicenses(work, []string{"./cmd/crap", "./cmd/crap-mutate"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"LICENSE": false,
		"licenses/github.com/go-git/go-git/v5/LICENSE":            false,
		"licenses/github.com/modelcontextprotocol/go-sdk/LICENSE": false,
	}
	for _, name := range names {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing staged license %s", name)
		}
	}
}

func TestArchiveMode(t *testing.T) {
	for _, name := range []string{"crap", "crap.exe", "crap-mutate", "crap-mutate.exe"} {
		if got := archiveMode(name); got != 0o755 {
			t.Errorf("archiveMode(%q) = %o, want 755", name, got)
		}
	}
	for _, name := range []string{"LICENSE", "licenses/example/NOTICE"} {
		if got := archiveMode(name); got != 0o644 {
			t.Errorf("archiveMode(%q) = %o, want 644", name, got)
		}
	}
}
