package rootauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyAuthorizesConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	allowed := t.TempDir()
	policy, err := New(root, allowed)
	if err != nil {
		t.Fatal(err)
	}
	defaultScope, err := policy.Root("")
	if err != nil || defaultScope.Path() == "" {
		t.Fatalf("default scope = %#v, %v", defaultScope, err)
	}
	if _, err := policy.Root(allowed); err != nil {
		t.Fatalf("allowed root rejected: %v", err)
	}
	if _, err := policy.Root(t.TempDir()); err == nil {
		t.Fatal("outside root was accepted")
	}
}

func TestRootRejectsLexicalAndSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	policy, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := policy.Root("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Existing(filepath.Join("..", filepath.Base(outside))); err == nil {
		t.Fatal("lexical escape was accepted")
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := scope.Existing(link); err == nil {
		t.Fatal("existing symlink escape was accepted")
	}
	if _, err := scope.Future(filepath.Join(link, "report.json")); err == nil {
		t.Fatal("future symlink escape was accepted")
	}
}

func TestRootAuthorizesExistingAndFuturePaths(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "src", "work.ts")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := policy.Root("")
	if err != nil {
		t.Fatal(err)
	}
	canonicalFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := scope.Existing(filepath.Join("src", "work.ts")); err != nil || got != canonicalFile {
		t.Fatalf("Existing = %q, %v, want %q", got, err, canonicalFile)
	}
	want := filepath.Join(scope.Path(), "reports", "mutation.json")
	if got, err := scope.Future(filepath.Join("reports", "mutation.json")); err != nil || got != want {
		t.Fatalf("Future = %q, %v, want %q", got, err, want)
	}
}
