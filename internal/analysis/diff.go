package analysis

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -(?:\d+)(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

type changedLines map[string]map[int]struct{}

func gitChangedLines(root, base string) (changedLines, error) {
	if base == "" {
		return nil, nil
	}
	output, err := gitOutput(root, "diff", "--unified=0", "--no-color", base, "--", "*.cs", "*.go")
	if err != nil {
		return nil, fmt.Errorf("git diff %q: %w", base, err)
	}
	result := parseDiff(string(output))
	untracked, err := gitOutput(root, "ls-files", "-z", "--others", "--exclude-standard", "--", "*.cs", "*.go")
	if err != nil {
		return nil, fmt.Errorf("list untracked source files: %w", err)
	}
	if err := addUntrackedFiles(result, root, untracked); err != nil {
		return nil, err
	}
	return result, nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
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

func addUntrackedFiles(result changedLines, root string, output []byte) error {
	for _, path := range strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		if err := addUntrackedFile(result, root, path); err != nil {
			return err
		}
	}
	return nil
}

func addUntrackedFile(result changedLines, root, path string) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return fmt.Errorf("read untracked file %s: %w", path, err)
	}
	lines := make(map[int]struct{})
	for line := 1; line <= strings.Count(string(data), "\n")+1; line++ {
		lines[line] = struct{}{}
	}
	result[normalizePath(path)] = lines
	return nil
}

func parseDiff(diff string) changedLines {
	result := make(changedLines)
	var currentFile string
	scanner := bufio.NewScanner(strings.NewReader(diff))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = normalizePath(strings.TrimPrefix(line, "+++ b/"))
			if result[currentFile] == nil {
				result[currentFile] = make(map[int]struct{})
			}
			continue
		}
		match := hunkHeader.FindStringSubmatch(line)
		if currentFile == "" || match == nil {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		for number := start; number < start+count; number++ {
			result[currentFile][number] = struct{}{}
		}
	}
	return result
}

func (changes changedLines) intersects(path string, start, end int) bool {
	lines := changes[normalizePath(path)]
	for line := range lines {
		if line >= start && line <= end {
			return true
		}
	}
	return false
}
