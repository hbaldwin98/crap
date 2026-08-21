package analysis

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hbaldwin98/crap/internal/rootauth"
)

func TestFindSourceFilesExcludesGoTestsByDefault(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"main.go", "main_test.go", "Example.cs", "app.ts", "app.spec.ts", "view.tsx", "view.test.tsx", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	discovery, err := findSourceFiles(root, []string{"."}, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := discovery.files
	if len(files) != 4 {
		t.Fatalf("default files = %v, want Go, C#, TypeScript, and TSX source", files)
	}
	discovery, err = findSourceFiles(root, []string{"."}, nil, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	files = discovery.files
	if len(files) != 7 {
		t.Fatalf("files with tests = %v, want seven source files", files)
	}
}

func TestFindSourceFilesHonorsIgnoreAndGeneratedPolicies(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{
		"keep.go", "ignored.go", "custom.go", "excluded.go", "work_test.go", "generated.g.cs",
		"generated.g.i.cs", "generated.AssemblyInfo.cs", "nested/keep.ts", "nested/ignored.ts",
		"node_modules/dependency.ts", "vendor/dependency.go",
	} {
		write(name)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\nnested/*.ts\n!nested/keep.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".crapignore"), []byte("custom.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	discovery, err := findSourceFiles(root, []string{"."}, []string{"excluded.go"}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := relativePaths(t, root, discovery.files)
	want := []string{"keep.go", "nested/keep.ts"}
	if !slices.Equal(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for _, reason := range []string{"built-in", "crapignore", "explicit", "generated", "gitignore", "test"} {
		if discovery.exclusions[reason] == 0 {
			t.Errorf("missing %s exclusion: %#v", reason, discovery.exclusions)
		}
	}
	if len(discovery.metadata().Exclusions) != 6 {
		t.Fatalf("discovery metadata = %#v", discovery.metadata())
	}

	discovery, err = findSourceFiles(root, []string{"generated.g.cs", "work_test.go"}, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := relativePaths(t, root, discovery.files); !slices.Equal(got, []string{"generated.g.cs", "work_test.go"}) {
		t.Fatalf("explicit files = %v", got)
	}

	discovery, err = findSourceFiles(root, []string{"."}, nil, true, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	got = relativePaths(t, root, discovery.files)
	if !slices.Contains(got, "generated.g.cs") || !slices.Contains(got, "work_test.go") {
		t.Fatalf("opted-in files = %v", got)
	}
}

func TestFindSourceFilesHandlesEscapedLeadingIgnoreCharacters(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"!important.go", "#generated.go", "keep.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("\\!important.go\n\\#generated.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	discovery, err := findSourceFiles(root, []string{"."}, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := relativePaths(t, root, discovery.files); !slices.Equal(got, []string{"keep.go"}) {
		t.Fatalf("files = %v", got)
	}
}

func TestFindSourceFilesDoesNotFollowIgnoreSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.go"), []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.ignore")
	if err := os.WriteFile(outside, []byte("*.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".crapignore")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	policy, err := rootauth.New(root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := policy.Root("")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := findSourceFiles(root, []string{"."}, nil, false, false, scope)
	if err != nil {
		t.Fatal(err)
	}
	if got := relativePaths(t, root, discovery.files); !slices.Equal(got, []string{"keep.go"}) {
		t.Fatalf("ignore symlink was followed: %v", got)
	}
}

func TestFindSourceFilesRejectsUnsupportedExplicitFileAndInvalidExclude(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findSourceFiles(root, []string{"notes.txt"}, nil, false, false, nil); err == nil {
		t.Fatal("unsupported explicit file was accepted")
	}
	if _, err := findSourceFiles(root, []string{"notes.txt"}, []string{"notes.txt"}, false, false, nil); err == nil {
		t.Fatal("excluded unsupported explicit file was accepted")
	}
	if _, err := findSourceFiles(root, []string{"."}, []string{"!keep.go"}, false, false, nil); err == nil {
		t.Fatal("negated explicit exclusion was accepted")
	}
}

func TestFindSourceFilesUsesParentAndNestedGitignoreWithoutScanningUnrelatedTrees(t *testing.T) {
	repositoryRoot := t.TempDir()
	runGit(t, repositoryRoot, "init")
	root := filepath.Join(repositoryRoot, "src")
	write := func(name string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package sample\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("parent-ignored.go")
	write("keep.go")
	write("nested/ignored.go")
	write("nested/keep.go")
	if err := os.WriteFile(filepath.Join(repositoryRoot, ".gitignore"), []byte("src/parent-ignored.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", ".gitignore"), []byte("ignored.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	discovery, err := findSourceFiles(root, []string{"nested", "keep.go"}, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := relativePaths(t, root, discovery.files); !slices.Equal(got, []string{"keep.go", "nested/keep.go"}) {
		t.Fatalf("files = %v", got)
	}

	discovery, err = findSourceFiles(root, []string{"."}, nil, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := relativePaths(t, root, discovery.files); !slices.Equal(got, []string{"keep.go", "nested/keep.go"}) {
		t.Fatalf("files with parent rules = %v", got)
	}
}

func relativePaths(t *testing.T, root string, files []string) []string {
	t.Helper()
	result := make([]string, len(files))
	for index, file := range files {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatal(err)
		}
		result[index] = filepath.ToSlash(relative)
	}
	return result
}

func TestFindSourceFilesRejectsSourcesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findSourceFiles(root, []string{outside}, nil, false, false, nil); err == nil {
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
	if _, err := findSourceFiles(root, []string{"link.go"}, nil, false, false, nil); err == nil {
		t.Fatal("outside source symlink was accepted")
	}
}
