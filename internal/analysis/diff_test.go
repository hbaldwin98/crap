package analysis

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseDiffRecordsCurrentRangesAndDeletionAnchors(t *testing.T) {
	diff := `diff --git a/src/Example.cs b/src/Example.cs
--- a/src/Example.cs
+++ b/src/Example.cs
@@ -2,2 +2,3 @@
-old one
-old two
+new one
+new two
+new three
@@ -10 +11,0 @@
-deleted
`
	changes, err := parseDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	want := []lineRange{{Start: 2, End: 4}, {Start: 11, End: 11}}
	if !reflect.DeepEqual(changes["src/Example.cs"], want) {
		t.Fatalf("ranges = %#v, want %#v", changes["src/Example.cs"], want)
	}
}

func TestParseDiffHandlesQuotedRenamedAndDeletedPaths(t *testing.T) {
	diff := `diff --git "a/old name.go" "b/new name.go"
--- "a/old name.go"
+++ "b/new name.go"
@@ -1 +1 @@
-old
+new
diff --git a/gone.go b/gone.go
--- a/gone.go
+++ /dev/null
@@ -1 +0,0 @@
-gone
`
	changes, err := parseDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changes["new name.go"], []lineRange{{Start: 1, End: 1}}) {
		t.Fatalf("changes = %#v", changes)
	}
	if _, exists := changes["gone.go"]; exists {
		t.Fatalf("deleted file must not have current ranges: %#v", changes)
	}
}

func TestParseDiffMergesRangesAndRejectsMalformedInput(t *testing.T) {
	diff := "+++ b/work.go\n@@ -1 +3,2 @@\n-old\n+one\n+two\n@@ -4 +5,2 @@\n-old\n+three\n+four\n"
	changes, err := parseDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changes["work.go"], []lineRange{{Start: 3, End: 6}}) {
		t.Fatalf("changes = %#v", changes)
	}
	if _, err := parseDiff("+++ b/work.go\n@@ invalid @@\n"); err == nil {
		t.Fatal("expected malformed hunk error")
	}
	if _, err := parseDiff("+++ \"unterminated\n"); err == nil {
		t.Fatal("expected malformed path error")
	}
}

func TestParseDiffDoesNotTreatAddedContentAsAFileHeader(t *testing.T) {
	diff := "+++ b/work.go\n@@ -1 +1,2 @@\n-old\n+++ counter\n+return\n"
	changes, err := parseDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changes["work.go"], []lineRange{{Start: 1, End: 2}}) {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestChangedFilesIntersectsRepositoryRelativePath(t *testing.T) {
	repositoryRoot := t.TempDir()
	changes := changedFiles{
		RepositoryRoot: repositoryRoot,
		Files: map[string][]lineRange{
			"internal/work.go": {{Start: 4, End: 6}, {Start: 10, End: 12}},
		},
	}
	path := filepath.Join(repositoryRoot, "internal", "work.go")
	if !changes.intersects(path, 6, 9) || changes.intersects(path, 7, 9) {
		t.Fatalf("unexpected intersection result for %#v", changes.Files)
	}
	if changes.intersects(filepath.Join(filepath.Dir(repositoryRoot), "work.go"), 1, 20) {
		t.Fatal("source outside repository must not match")
	}
}

func TestGitSourcePathspecsAreLiteralAndSorted(t *testing.T) {
	repositoryRoot := t.TempDir()
	files := []string{
		filepath.Join(repositoryRoot, "[scope]", "work?.go"),
		filepath.Join(repositoryRoot, "a.go"),
	}
	pathspecs, err := gitSourcePathspecs(repositoryRoot, files)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{":(top,literal)[scope]/work?.go", ":(top,literal)a.go"}
	if !reflect.DeepEqual(pathspecs, want) {
		t.Fatalf("pathspecs = %#v, want %#v", pathspecs, want)
	}
}

func TestBatchPathspecsBoundsCommandSize(t *testing.T) {
	pathspecs := make([]string, 300)
	for index := range pathspecs {
		pathspecs[index] = strings.Repeat("x", 100)
	}
	batches := batchPathspecs(pathspecs)
	total := 0
	for _, batch := range batches {
		total += len(batch)
		bytes := 0
		for _, pathspec := range batch {
			bytes += len(pathspec) + 1
		}
		if len(batch) > maxPathspecsPerBatch || bytes > maxPathspecBytes {
			t.Fatalf("oversized batch: files=%d bytes=%d", len(batch), bytes)
		}
	}
	if total != len(pathspecs) || len(batches) < 2 {
		t.Fatalf("batches=%d total=%d", len(batches), total)
	}
}

func TestGitChangedLinesUsesRepositoryRootAndMergeBase(t *testing.T) {
	repositoryRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repositoryRoot, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(repositoryRoot, "internal", "work.go")
	writeDiffTestFile(t, tracked, "package internal\n\nfunc work() {\n\tprintln(1)\n\tprintln(2)\n}\n")
	runGit(t, repositoryRoot, "init")
	runGit(t, repositoryRoot, "add", ".")
	runGit(t, repositoryRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	runGit(t, repositoryRoot, "checkout", "-b", "feature")
	writeDiffTestFile(t, filepath.Join(repositoryRoot, "feature.go"), "package feature\n")
	runGit(t, repositoryRoot, "add", ".")
	runGit(t, repositoryRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "feature")
	runGit(t, repositoryRoot, "checkout", "-")
	writeDiffTestFile(t, filepath.Join(repositoryRoot, "base.go"), "package base\n")
	runGit(t, repositoryRoot, "add", ".")
	runGit(t, repositoryRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "base")
	baseCommit := gitTestValue(t, repositoryRoot, "rev-parse", "HEAD")
	runGit(t, repositoryRoot, "checkout", "feature")
	headCommit := gitTestValue(t, repositoryRoot, "rev-parse", "HEAD")
	mergeBase := gitTestValue(t, repositoryRoot, "merge-base", baseCommit, headCommit)
	writeDiffTestFile(t, tracked, "package internal\n\nfunc work() {\n\tprintln(2)\n}\n")
	untracked := filepath.Join(repositoryRoot, "internal", "new file.go")
	writeDiffTestFile(t, untracked, "package internal\n\nfunc added() {}\n")
	ignoredUntracked := filepath.Join(repositoryRoot, "internal", "explicit.go")
	writeDiffTestFile(t, ignoredUntracked, "package internal\n\nfunc explicit() {}\n")
	writeDiffTestFile(t, filepath.Join(repositoryRoot, ".gitignore"), "internal/explicit.go\n")

	changes, err := gitChangedLines(filepath.Join(repositoryRoot, "internal"), baseCommit, []string{tracked, untracked, ignoredUntracked}, execGitRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if changes.RepositoryRoot != repositoryRoot || changes.BaseCommit != baseCommit || changes.HeadCommit != headCommit || changes.MergeBase != mergeBase || changes.BaseCommit == changes.MergeBase {
		t.Fatalf("unexpected repository metadata: %#v", changes)
	}
	if !changes.intersects(tracked, 3, 5) {
		t.Fatalf("deletion-only edit did not intersect callable: %#v", changes.Files)
	}
	if !changes.intersects(untracked, 3, 3) {
		t.Fatalf("untracked source did not intersect callable: %#v", changes.Files)
	}
	if !changes.intersects(ignoredUntracked, 3, 3) {
		t.Fatalf("explicit ignored source did not intersect callable: %#v", changes.Files)
	}
}

func TestGitChangedLinesRejectsRevisionOptions(t *testing.T) {
	repositoryRoot := t.TempDir()
	runGit(t, repositoryRoot, "init")
	writeDiffTestFile(t, filepath.Join(repositoryRoot, "work.go"), "package work\n")
	runGit(t, repositoryRoot, "add", ".")
	runGit(t, repositoryRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	_, err := gitChangedLines(repositoryRoot, "--output=owned", []string{filepath.Join(repositoryRoot, "work.go")}, execGitRunner{})
	if err == nil || !strings.Contains(err.Error(), "resolve diff base") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repositoryRoot, "owned")); !os.IsNotExist(statErr) {
		t.Fatalf("revision was interpreted as an option: %v", statErr)
	}
}

func TestGitChangedLinesTreatsRenameDestinationAsChanged(t *testing.T) {
	repositoryRoot := t.TempDir()
	oldPath := filepath.Join(repositoryRoot, "old.go")
	newPath := filepath.Join(repositoryRoot, "new.go")
	writeDiffTestFile(t, oldPath, "package work\n\nfunc moved() {}\n")
	runGit(t, repositoryRoot, "init")
	runGit(t, repositoryRoot, "add", ".")
	runGit(t, repositoryRoot, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "baseline")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	changes, err := gitChangedLines(repositoryRoot, "HEAD", []string{newPath}, execGitRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if !changes.intersects(newPath, 3, 3) {
		t.Fatalf("renamed destination was not treated as changed: %#v", changes.Files)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func gitTestValue(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeDiffTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
