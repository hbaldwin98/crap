package sarif

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type Source struct {
	path  string
	lines [][]byte
}

func ReadSource(root, displayedPath string) (*Source, error) {
	canonicalRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	canonicalRoot, err = filepath.EvalSymlinks(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve root links: %w", err)
	}
	normalized := path.Clean(strings.ReplaceAll(displayedPath, "\\", "/"))
	if normalized == "" || normalized == "." || path.IsAbs(normalized) || normalized == ".." || strings.HasPrefix(normalized, "../") || filepath.VolumeName(filepath.FromSlash(normalized)) != "" {
		return nil, fmt.Errorf("source path must be root-relative: %q", displayedPath)
	}
	candidate := filepath.Join(canonicalRoot, filepath.FromSlash(normalized))
	relative, err := filepath.Rel(canonicalRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("source path is outside root: %q", displayedPath)
	}
	current := canonicalRoot
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, fmt.Errorf("inspect source %q: %w", displayedPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("source path contains a symlink: %q", displayedPath)
		}
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return nil, fmt.Errorf("inspect source %q: %w", displayedPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source is not a regular file: %q", displayedPath)
	}
	data, err := os.ReadFile(candidate)
	if err != nil {
		return nil, fmt.Errorf("read source %q: %w", displayedPath, err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("source is not valid UTF-8: %q", displayedPath)
	}
	lines := bytes.Split(data, []byte{'\n'})
	for index := range lines {
		lines[index] = bytes.TrimSuffix(lines[index], []byte{'\r'})
	}
	return &Source{path: normalized, lines: lines}, nil
}

func (source *Source) URI() string { return URI(source.path) }

func (source *Source) ByteRegion(startLine, startColumn, endLine, endColumn int) (Region, error) {
	start, err := source.byteColumn(startLine, startColumn)
	if err != nil {
		return Region{}, fmt.Errorf("start coordinate: %w", err)
	}
	end, err := source.byteColumn(endLine, endColumn)
	if err != nil {
		return Region{}, fmt.Errorf("end coordinate: %w", err)
	}
	region := Region{StartLine: startLine, StartColumn: start, EndLine: endLine, EndColumn: end}
	if err := validateRange(region); err != nil {
		return Region{}, err
	}
	return region, nil
}

func (source *Source) UTF16Region(startLine, startColumn, endLine, endColumn int) (Region, error) {
	if err := source.validateUTF16Column(startLine, startColumn); err != nil {
		return Region{}, fmt.Errorf("start coordinate: %w", err)
	}
	if err := source.validateUTF16Column(endLine, endColumn); err != nil {
		return Region{}, fmt.Errorf("end coordinate: %w", err)
	}
	region := Region{StartLine: startLine, StartColumn: startColumn, EndLine: endLine, EndColumn: endColumn}
	if err := validateRange(region); err != nil {
		return Region{}, err
	}
	return region, nil
}

func (source *Source) UTF16PointRegion(line, column int) (Region, error) {
	content, err := source.line(line)
	if err != nil {
		return Region{}, err
	}
	if err := source.validateUTF16Column(line, column); err != nil {
		return Region{}, err
	}
	boundaries := []int{1}
	position := 1
	for _, value := range string(content) {
		position++
		if value > 0xffff {
			position++
		}
		boundaries = append(boundaries, position)
	}
	for index, boundary := range boundaries {
		if boundary != column {
			continue
		}
		if index+1 < len(boundaries) {
			return Region{StartLine: line, StartColumn: column, EndLine: line, EndColumn: boundaries[index+1]}, nil
		}
		if index > 0 {
			return Region{StartLine: line, StartColumn: boundaries[index-1], EndLine: line, EndColumn: column}, nil
		}
		break
	}
	return Region{}, fmt.Errorf("cannot represent a point on empty line %d as a SARIF region", line)
}

func (source *Source) BytePointRegion(line, column int) (Region, error) {
	content, err := source.line(line)
	if err != nil {
		return Region{}, err
	}
	if column < 1 || column > len(content) {
		return Region{}, fmt.Errorf("point column %d is outside line %d", column, line)
	}
	start, err := source.byteColumn(line, column)
	if err != nil {
		return Region{}, err
	}
	_, size := utf8.DecodeRune(content[column-1:])
	end, err := source.byteColumn(line, column+size)
	if err != nil {
		return Region{}, err
	}
	return Region{StartLine: line, StartColumn: start, EndLine: line, EndColumn: end}, nil
}

func (source *Source) byteColumn(lineNumber, column int) (int, error) {
	content, err := source.line(lineNumber)
	if err != nil {
		return 0, err
	}
	if column < 1 || column > len(content)+1 {
		return 0, fmt.Errorf("byte column %d is outside line %d", column, lineNumber)
	}
	offset := column - 1
	if offset < len(content) && !utf8.RuneStart(content[offset]) {
		return 0, fmt.Errorf("byte column %d splits a UTF-8 sequence on line %d", column, lineNumber)
	}
	return len(utf16.Encode([]rune(string(content[:offset])))) + 1, nil
}

func (source *Source) validateUTF16Column(lineNumber, column int) error {
	content, err := source.line(lineNumber)
	if err != nil {
		return err
	}
	if column < 1 {
		return fmt.Errorf("UTF-16 column %d is outside line %d", column, lineNumber)
	}
	position := 1
	if column == position {
		return nil
	}
	for _, value := range string(content) {
		position += 1
		if value > 0xffff {
			position++
		}
		if column == position {
			return nil
		}
	}
	return fmt.Errorf("UTF-16 column %d is outside or splits a surrogate pair on line %d", column, lineNumber)
}

func (source *Source) line(number int) ([]byte, error) {
	if number < 1 || number > len(source.lines) {
		return nil, fmt.Errorf("line %d is outside source", number)
	}
	return source.lines[number-1], nil
}

func validateRange(region Region) error {
	if region.EndLine < region.StartLine || (region.EndLine == region.StartLine && region.EndColumn < region.StartColumn) {
		return fmt.Errorf("end coordinate precedes start coordinate")
	}
	return nil
}
