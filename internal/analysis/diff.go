package analysis

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hbaldwin98/crap/internal/rootauth"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

type lineRange struct {
	Start int
	End   int
}

type changedFiles struct {
	RepositoryRoot string
	BaseCommit     string
	HeadCommit     string
	MergeBase      string
	Files          map[string][]lineRange
}

type gitRunner interface {
	Output(string, ...string) ([]byte, error)
}

type execGitRunner struct{}

func (execGitRunner) Output(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(exit.Stderr)))
	}
	if err != nil {
		return nil, fmt.Errorf("run git: %w", err)
	}
	return output, nil
}

func gitChangedLines(root, base string, sourceFiles []string, runner gitRunner, authorization ...*rootauth.Root) (changedFiles, error) {
	if base == "" {
		return changedFiles{}, nil
	}
	repositoryRoot, err := gitValue(runner, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return changedFiles{}, fmt.Errorf("discover Git repository from %s: %w", root, err)
	}
	repositoryRoot, err = filepath.Abs(repositoryRoot)
	if err != nil {
		return changedFiles{}, fmt.Errorf("resolve Git repository root: %w", err)
	}
	if len(authorization) > 0 && authorization[0] != nil {
		repositoryRoot, err = authorization[0].Existing(repositoryRoot)
		if err != nil {
			return changedFiles{}, fmt.Errorf("authorize Git repository: %w", err)
		}
	}
	baseCommit, err := gitValue(runner, repositoryRoot, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return changedFiles{}, fmt.Errorf("resolve diff base %q as a commit: %w", base, err)
	}
	headCommit, err := gitValue(runner, repositoryRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return changedFiles{}, fmt.Errorf("resolve HEAD as a commit: %w", err)
	}
	mergeBase, err := gitValue(runner, repositoryRoot, "merge-base", baseCommit, headCommit)
	if err != nil {
		return changedFiles{}, fmt.Errorf("find merge base between %s and %s: %w", baseCommit, headCommit, err)
	}
	pathspecs, err := gitSourcePathspecs(repositoryRoot, sourceFiles)
	if err != nil {
		return changedFiles{}, err
	}
	files := make(map[string][]lineRange)
	if len(pathspecs) == 0 {
		return changedFiles{RepositoryRoot: repositoryRoot, BaseCommit: baseCommit, HeadCommit: headCommit, MergeBase: mergeBase, Files: files}, nil
	}
	for _, batch := range batchPathspecs(pathspecs) {
		diffArgs := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", "--unified=0", "--no-color", mergeBase, "--"}
		diffArgs = append(diffArgs, batch...)
		output, err := runner.Output(repositoryRoot, diffArgs...)
		if err != nil {
			return changedFiles{}, fmt.Errorf("diff from merge base %s: %w", mergeBase, err)
		}
		parsed, err := parseDiff(string(output))
		if err != nil {
			return changedFiles{}, fmt.Errorf("parse Git diff: %w", err)
		}
		for filename, ranges := range parsed {
			files[filename] = mergeRanges(append(files[filename], ranges...))
		}
		// Discovery already applied ignore policy. Ask Git for every selected
		// untracked source so explicitly requested ignored files remain changed.
		untrackedArgs := []string{"ls-files", "-z", "--others", "--"}
		untrackedArgs = append(untrackedArgs, batch...)
		untracked, err := runner.Output(repositoryRoot, untrackedArgs...)
		if err != nil {
			return changedFiles{}, fmt.Errorf("list untracked source files: %w", err)
		}
		if err := addUntrackedFiles(files, repositoryRoot, untracked); err != nil {
			return changedFiles{}, err
		}
	}
	return changedFiles{
		RepositoryRoot: repositoryRoot,
		BaseCommit:     baseCommit,
		HeadCommit:     headCommit,
		MergeBase:      mergeBase,
		Files:          files,
	}, nil
}

const (
	maxPathspecsPerBatch = 128
	maxPathspecBytes     = 12 * 1024
)

func batchPathspecs(pathspecs []string) [][]string {
	batches := make([][]string, 0, (len(pathspecs)+maxPathspecsPerBatch-1)/maxPathspecsPerBatch)
	for len(pathspecs) > 0 {
		count, bytes := 0, 0
		for count < len(pathspecs) && count < maxPathspecsPerBatch {
			next := len(pathspecs[count]) + 1
			if count > 0 && bytes+next > maxPathspecBytes {
				break
			}
			bytes += next
			count++
		}
		batches = append(batches, pathspecs[:count])
		pathspecs = pathspecs[count:]
	}
	return batches
}

func gitSourcePathspecs(repositoryRoot string, sourceFiles []string) ([]string, error) {
	unique := make(map[string]struct{}, len(sourceFiles))
	for _, sourceFile := range sourceFiles {
		relative, err := filepath.Rel(repositoryRoot, sourceFile)
		if err != nil {
			return nil, fmt.Errorf("make source %s repository-relative: %w", sourceFile, err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("source %s is outside Git repository %s", sourceFile, repositoryRoot)
		}
		unique[":(top,literal)"+normalizeGitPath(relative)] = struct{}{}
	}
	pathspecs := make([]string, 0, len(unique))
	for pathspec := range unique {
		pathspecs = append(pathspecs, pathspec)
	}
	sort.Strings(pathspecs)
	return pathspecs, nil
}

func gitValue(runner gitRunner, root string, args ...string) (string, error) {
	output, err := runner.Output(root, args...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("Git returned an empty value")
	}
	return value, nil
}

func addUntrackedFiles(result map[string][]lineRange, repositoryRoot string, output []byte) error {
	for _, filename := range strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00") {
		if filename == "" {
			continue
		}
		if !supportedSource(filename) {
			continue
		}
		if err := addUntrackedFile(result, repositoryRoot, filename); err != nil {
			return err
		}
	}
	return nil
}

func addUntrackedFile(result map[string][]lineRange, repositoryRoot, filename string) error {
	data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(filename)))
	if err != nil {
		return fmt.Errorf("read untracked source %s: %w", filename, err)
	}
	lineCount := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lineCount++
	}
	if lineCount > 0 {
		key := normalizeGitPath(filename)
		result[key] = mergeRanges(append(result[key], lineRange{Start: 1, End: lineCount}))
	}
	return nil
}

func parseDiff(diff string) (map[string][]lineRange, error) {
	result := make(map[string][]lineRange)
	var currentFile string
	oldRemaining, newRemaining := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if oldRemaining > 0 || newRemaining > 0 {
			switch {
			case strings.HasPrefix(line, "\\ No newline at end of file"):
			case strings.HasPrefix(line, "+"):
				newRemaining--
			case strings.HasPrefix(line, "-"):
				oldRemaining--
			default:
				oldRemaining--
				newRemaining--
			}
			if oldRemaining < 0 || newRemaining < 0 {
				return nil, fmt.Errorf("hunk body exceeds declared line counts")
			}
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			filename, deleted, err := parseGitDestination(line)
			if err != nil {
				return nil, err
			}
			if deleted {
				currentFile = ""
				continue
			}
			currentFile = filename
			continue
		}
		match := hunkHeader.FindStringSubmatch(line)
		if strings.HasPrefix(line, "@@ ") && match == nil {
			return nil, fmt.Errorf("invalid hunk header %q", line)
		}
		if currentFile == "" || match == nil {
			continue
		}
		oldCount, err := hunkCount(match[2])
		if err != nil {
			return nil, err
		}
		start, err := strconv.Atoi(match[3])
		if err != nil {
			return nil, fmt.Errorf("invalid hunk start %q", match[3])
		}
		count, err := hunkCount(match[4])
		if err != nil {
			return nil, err
		}
		oldRemaining, newRemaining = oldCount, count
		if count == 0 {
			if start < 1 {
				start = 1
			}
			result[currentFile] = append(result[currentFile], lineRange{Start: start, End: start})
			continue
		}
		result[currentFile] = append(result[currentFile], lineRange{Start: start, End: start + count - 1})
	}
	if oldRemaining != 0 || newRemaining != 0 {
		return nil, fmt.Errorf("hunk body ended before declared line counts")
	}
	for filename, ranges := range result {
		result[filename] = mergeRanges(ranges)
	}
	return result, nil
}

func hunkCount(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid hunk count %q", value)
	}
	return count, nil
}

func parseGitDestination(header string) (string, bool, error) {
	value := strings.TrimPrefix(header, "+++ ")
	if value == "/dev/null" {
		return "", true, nil
	}
	if strings.HasPrefix(value, `"`) {
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", false, fmt.Errorf("decode Git path %q: %w", value, err)
		}
		value = decoded
	}
	if !strings.HasPrefix(value, "b/") {
		return "", false, fmt.Errorf("invalid Git destination %q", value)
	}
	return normalizeGitPath(strings.TrimPrefix(value, "b/")), false, nil
}

func mergeRanges(ranges []lineRange) []lineRange {
	if len(ranges) < 2 {
		return ranges
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Start == ranges[j].Start {
			return ranges[i].End < ranges[j].End
		}
		return ranges[i].Start < ranges[j].Start
	})
	merged := []lineRange{ranges[0]}
	for _, current := range ranges[1:] {
		last := &merged[len(merged)-1]
		if current.Start <= last.End+1 {
			if current.End > last.End {
				last.End = current.End
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func (changes changedFiles) intersects(sourcePath string, start, end int) bool {
	if changes.RepositoryRoot == "" {
		return false
	}
	relative, err := filepath.Rel(changes.RepositoryRoot, sourcePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	for _, changed := range changes.Files[normalizeGitPath(relative)] {
		if changed.Start > end {
			return false
		}
		if changed.End >= start {
			return true
		}
	}
	return false
}

func normalizeGitPath(value string) string {
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(value, "\\", "/")), "./")
}
