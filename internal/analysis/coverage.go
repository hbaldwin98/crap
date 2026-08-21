package analysis

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type coverageSpan struct {
	StartLine  int
	EndLine    int
	Statements int
	Covered    bool
}

type coverageData map[string][]coverageSpan

type cobertura struct {
	Classes []struct {
		Filename string `xml:"filename,attr"`
		Lines    []struct {
			Number int    `xml:"number,attr"`
			Hits   string `xml:"hits,attr"`
		} `xml:"lines>line"`
	} `xml:"packages>package>classes>class"`
}

var goCoverageLine = regexp.MustCompile(`^(.+):(\d+)\.\d+,(\d+)\.\d+\s+(\d+)\s+(\d+)$`)

func loadCoverage(path, root string) (coverageData, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read coverage: %w", err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("mode:")) {
		return parseGoCoverage(string(data))
	}
	return parseCobertura(data, root)
}

func parseCobertura(data []byte, root string) (coverageData, error) {
	var document cobertura
	if err := xml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse Cobertura coverage: %w", err)
	}
	merged := make(map[string]map[int]bool)
	for _, class := range document.Classes {
		filename := normalizePath(class.Filename)
		if filepath.IsAbs(class.Filename) {
			if relative, err := filepath.Rel(root, class.Filename); err == nil {
				filename = normalizePath(relative)
			}
		}
		if merged[filename] == nil {
			merged[filename] = make(map[int]bool)
		}
		for _, line := range class.Lines {
			hits, _ := strconv.Atoi(line.Hits)
			merged[filename][line.Number] = merged[filename][line.Number] || hits > 0
		}
	}
	result := make(coverageData)
	for filename, lines := range merged {
		for line, covered := range lines {
			result[filename] = append(result[filename], coverageSpan{StartLine: line, EndLine: line, Statements: 1, Covered: covered})
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("coverage report contains no Cobertura class lines")
	}
	return result, nil
}

func parseGoCoverage(content string) (coverageData, error) {
	result := make(coverageData)
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if index == 0 && strings.HasPrefix(line, "mode:") {
			continue
		}
		match := goCoverageLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("parse Go coverprofile line %d: %q", index+1, line)
		}
		start, _ := strconv.Atoi(match[2])
		end, _ := strconv.Atoi(match[3])
		statements, _ := strconv.Atoi(match[4])
		count, _ := strconv.Atoi(match[5])
		filename := normalizePath(match[1])
		result[filename] = append(result[filename], coverageSpan{
			StartLine: start, EndLine: end, Statements: statements, Covered: count > 0,
		})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("Go coverprofile contains no coverage blocks")
	}
	return result, nil
}

func (coverage coverageData) forFile(path string) []coverageSpan {
	normalized := normalizePath(path)
	if spans := coverage[normalized]; spans != nil {
		return spans
	}
	var match []coverageSpan
	found := false
	for candidate, spans := range coverage {
		if strings.HasSuffix(normalized, "/"+candidate) || strings.HasSuffix(candidate, "/"+normalized) {
			if found {
				// Ambiguous suffixes are safer to report as unknown than to pick by map order.
				return nil
			}
			match = spans
			found = true
		}
	}
	return match
}

func methodCoverage(spans []coverageSpan, start, end int) *float64 {
	if spans == nil {
		return nil
	}
	total, covered := 0, 0
	for _, span := range spans {
		if span.EndLine < start || span.StartLine > end {
			continue
		}
		total += span.Statements
		if span.Covered {
			covered += span.Statements
		}
	}
	if total == 0 {
		return nil
	}
	percent := round(float64(covered)*100/float64(total), 2)
	return &percent
}
