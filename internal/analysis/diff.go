package analysis

import (
	"context"
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

type gitChangeRequest struct {
	repositoryRoot string
	baseCommit     string
	headCommit     string
	mergeBase      string
	pathspecs      []string
}

type gitRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type execGitRunner struct{}

func (execGitRunner) Output(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	output, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(exit.Stderr)))
	}
	if err != nil {
		return nil, fmt.Errorf("run git: %w", err)
	}
	return output, nil
}

func gitChangedLines(root, base string, sourceFiles []string, runner gitRunner, authorization ...*rootauth.Root) (changedFiles, error) {
	return gitChangedLinesContext(context.Background(), root, base, sourceFiles, runner, authorization...)
}

func gitChangedLinesContext(ctx context.Context, root, base string, sourceFiles []string, runner gitRunner, authorization ...*rootauth.Root) (changedFiles, error) {
	if err := ctx.Err(); err != nil {
		return changedFiles{}, err
	}
	if base == "" {
		return changedFiles{}, nil
	}
	request, err := prepareGitChangeRequest(ctx, root, base, sourceFiles, runner, authorization...)
	if err != nil {
		return changedFiles{}, err
	}
	files, err := collectGitChanges(ctx, request, runner)
	if err != nil {
		return changedFiles{}, err
	}
	return changedFiles{
		RepositoryRoot: request.repositoryRoot,
		BaseCommit:     request.baseCommit,
		HeadCommit:     request.headCommit,
		MergeBase:      request.mergeBase,
		Files:          files,
	}, nil
}

func prepareGitChangeRequest(ctx context.Context, root, base string, sourceFiles []string, runner gitRunner, authorization ...*rootauth.Root) (gitChangeRequest, error) {
	repositoryRoot, err := gitValue(ctx, runner, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitChangeRequest{}, fmt.Errorf("discover Git repository from %s: %w", root, err)
	}
	repositoryRoot, err = filepath.Abs(repositoryRoot)
	if err != nil {
		return gitChangeRequest{}, fmt.Errorf("resolve Git repository root: %w", err)
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return gitChangeRequest{}, fmt.Errorf("resolve Git repository root links: %w", err)
	}
	if len(authorization) > 0 && authorization[0] != nil {
		repositoryRoot, err = authorization[0].Existing(repositoryRoot)
		if err != nil {
			return gitChangeRequest{}, fmt.Errorf("authorize Git repository: %w", err)
		}
	}
	baseCommit, err := gitValue(ctx, runner, repositoryRoot, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return gitChangeRequest{}, fmt.Errorf("resolve diff base %q as a commit: %w", base, err)
	}
	headCommit, err := gitValue(ctx, runner, repositoryRoot, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return gitChangeRequest{}, fmt.Errorf("resolve HEAD as a commit: %w", err)
	}
	mergeBase, err := gitValue(ctx, runner, repositoryRoot, "merge-base", baseCommit, headCommit)
	if err != nil {
		return gitChangeRequest{}, fmt.Errorf("find merge base between %s and %s: %w", baseCommit, headCommit, err)
	}
	pathspecs, err := canonicalGitPathspecs(repositoryRoot, sourceFiles)
	if err != nil {
		return gitChangeRequest{}, err
	}
	return gitChangeRequest{repositoryRoot, baseCommit, headCommit, mergeBase, pathspecs}, nil
}

func canonicalGitPathspecs(repositoryRoot string, sourceFiles []string) ([]string, error) {
	canonicalSources := make([]string, len(sourceFiles))
	for index, sourceFile := range sourceFiles {
		canonical, err := filepath.EvalSymlinks(sourceFile)
		if err != nil {
			return nil, fmt.Errorf("resolve source %s for Git: %w", sourceFile, err)
		}
		canonicalSources[index] = canonical
	}
	return gitSourcePathspecs(repositoryRoot, canonicalSources)
}

func collectGitChanges(ctx context.Context, request gitChangeRequest, runner gitRunner) (map[string][]lineRange, error) {
	files := make(map[string][]lineRange)
	for _, batch := range batchPathspecs(request.pathspecs) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		diffArgs := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", "--unified=0", "--no-color", request.mergeBase, "--"}
		diffArgs = append(diffArgs, batch...)
		output, err := runner.Output(ctx, request.repositoryRoot, diffArgs...)
		if err != nil {
			return nil, fmt.Errorf("diff from merge base %s: %w", request.mergeBase, err)
		}
		parsed, err := parseDiffContext(ctx, string(output))
		if err != nil {
			return nil, fmt.Errorf("parse Git diff: %w", err)
		}
		for filename, ranges := range parsed {
			files[filename] = mergeRanges(append(files[filename], ranges...))
		}
		// Discovery already applied ignore policy. Ask Git for every selected
		// untracked source so explicitly requested ignored files remain changed.
		untrackedArgs := []string{"ls-files", "-z", "--others", "--"}
		untrackedArgs = append(untrackedArgs, batch...)
		untracked, err := runner.Output(ctx, request.repositoryRoot, untrackedArgs...)
		if err != nil {
			return nil, fmt.Errorf("list untracked source files: %w", err)
		}
		if err := addUntrackedFilesContext(ctx, files, request.repositoryRoot, untracked); err != nil {
			return nil, err
		}
	}
	return files, nil
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

func gitValue(ctx context.Context, runner gitRunner, root string, args ...string) (string, error) {
	output, err := runner.Output(ctx, root, args...)
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
	return addUntrackedFilesContext(context.Background(), result, repositoryRoot, output)
}

func addUntrackedFilesContext(ctx context.Context, result map[string][]lineRange, repositoryRoot string, output []byte) error {
	for _, filename := range strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00") {
		if err := ctx.Err(); err != nil {
			return err
		}
		if filename == "" {
			continue
		}
		if !supportedSource(filename) {
			continue
		}
		if err := addUntrackedFileContext(ctx, result, repositoryRoot, filename); err != nil {
			return err
		}
	}
	return nil
}

func addUntrackedFile(result map[string][]lineRange, repositoryRoot, filename string) error {
	return addUntrackedFileContext(context.Background(), result, repositoryRoot, filename)
}

func addUntrackedFileContext(ctx context.Context, result map[string][]lineRange, repositoryRoot, filename string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(filename)))
	if err != nil {
		return fmt.Errorf("read untracked source %s: %w", filename, err)
	}
	if err := ctx.Err(); err != nil {
		return err
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
	return parseDiffContext(context.Background(), diff)
}

func parseDiffContext(ctx context.Context, diff string) (map[string][]lineRange, error) {
	result := make(map[string][]lineRange)
	var currentFile string
	oldRemaining, newRemaining := 0, 0
	for _, line := range strings.Split(diff, "\n") {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	canonical := sourcePath
	if resolved, err := filepath.EvalSymlinks(sourcePath); err == nil {
		canonical = resolved
	}
	relative, err := filepath.Rel(changes.RepositoryRoot, canonical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	ranges := changes.Files[normalizeGitPath(relative)]
	index := sort.Search(len(ranges), func(index int) bool { return ranges[index].End >= start })
	return index < len(ranges) && ranges[index].Start <= end
}

func normalizeGitPath(value string) string {
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(value, "\\", "/")), "./")
}
