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

	command := exec.Command("git", "diff", "--unified=0", "--no-color", base, "--", "*.cs", "*.go")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git diff %q failed: %s", base, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("run git diff: %w", err)
	}

	result := parseDiff(string(output))
	untracked := exec.Command("git", "ls-files", "-z", "--others", "--exclude-standard", "--", "*.cs", "*.go")
	untracked.Dir = root
	untrackedOutput, err := untracked.Output()
	if err != nil {
		return nil, fmt.Errorf("list untracked source files: %w", err)
	}
	for _, path := range strings.Split(strings.TrimSuffix(string(untrackedOutput), "\x00"), "\x00") {
		if path == "" {
			continue
		}
		normalized := normalizePath(path)
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read untracked file %s: %w", path, err)
		}
		result[normalized] = make(map[int]struct{})
		lineCount := strings.Count(string(data), "\n") + 1
		for line := 1; line <= lineCount; line++ {
			result[normalized][line] = struct{}{}
		}
	}
	return result, nil
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
