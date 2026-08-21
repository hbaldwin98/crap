package analysis

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hbaldwin98/crap/internal/reportcontract"
)

type coverageSpan struct {
	StartLine  int
	EndLine    int
	Statements int
	Covered    bool
}

type coverageData struct {
	files   map[string][]coverageSpan
	aliases map[string][]string
	loaded  bool
	format  string
	sha256  string
}

type coverageMatch struct {
	spans      []coverageSpan
	kind       string
	candidates []string
	identities []string
}

type cobertura struct {
	Sources []string `xml:"sources>source"`
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
		return coverageData{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return coverageData{}, fmt.Errorf("read coverage: %w", err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("mode:")) {
		result, err := parseGoCoverage(string(data))
		result.sha256 = reportcontract.SHA256(data)
		return result, err
	}
	result, err := parseCobertura(data, root)
	result.sha256 = reportcontract.SHA256(data)
	return result, err
}

func parseCobertura(data []byte, root string) (coverageData, error) {
	var document cobertura
	if err := xml.Unmarshal(data, &document); err != nil {
		return coverageData{}, fmt.Errorf("parse Cobertura coverage: %w", err)
	}
	merged := make(map[string]map[int]bool)
	aliases := make(map[string][]string)
	for _, class := range document.Classes {
		filename, classAliases := coberturaIdentities(class.Filename, document.Sources, root)
		if filename == "." || filename == "" {
			return coverageData{}, fmt.Errorf("Cobertura class has an empty filename")
		}
		if merged[filename] == nil {
			merged[filename] = make(map[int]bool)
		}
		for _, alias := range classAliases {
			aliases[alias] = appendUnique(aliases[alias], filename)
		}
		for _, line := range class.Lines {
			if line.Number < 1 {
				return coverageData{}, fmt.Errorf("Cobertura class %q has invalid line %d", class.Filename, line.Number)
			}
			hits, err := strconv.Atoi(line.Hits)
			if err != nil || hits < 0 {
				return coverageData{}, fmt.Errorf("Cobertura class %q line %d has invalid hits %q", class.Filename, line.Number, line.Hits)
			}
			merged[filename][line.Number] = merged[filename][line.Number] || hits > 0
		}
	}
	result := coverageData{files: make(map[string][]coverageSpan), aliases: aliases, loaded: true, format: "cobertura"}
	for filename, lines := range merged {
		for line, covered := range lines {
			result.files[filename] = append(result.files[filename], coverageSpan{StartLine: line, EndLine: line, Statements: 1, Covered: covered})
		}
	}
	if len(result.files) == 0 {
		return coverageData{}, fmt.Errorf("coverage report contains no Cobertura class lines")
	}
	return result, nil
}

func coberturaIdentities(filename string, sources []string, root string) (string, []string) {
	filename = normalizePortablePath(filename)
	root = strings.TrimSuffix(normalizePortablePath(root), "/")
	if isPortableAbs(filename) {
		if relative, ok := portableRelative(root, filename); ok {
			return relative, []string{relative}
		}
		aliases := make([]string, 0, len(sources)+1)
		for _, source := range sources {
			source = strings.TrimSuffix(normalizePortablePath(source), "/")
			if isPortableAbs(source) {
				if relative, ok := portableRelative(source, filename); ok && safeCoverageIdentity(relative) {
					aliases = append(aliases, relative)
				}
			}
		}
		if len(aliases) == 0 {
			aliases = append(aliases, "_external/"+reportcontract.SHA256([]byte(filename))[:12]+"/"+path.Base(filename))
		}
		sort.Strings(aliases)
		aliases = slicesCompact(aliases)
		return aliases[0], aliases
	}
	aliases := []string{filename}
	for _, source := range sources {
		source = normalizePortablePath(source)
		candidate := normalizePortablePath(source + "/" + filename)
		if isPortableAbs(source) {
			if relative, ok := portableRelative(root, candidate); ok {
				aliases = append(aliases, relative)
			}
		} else {
			aliases = append(aliases, candidate)
		}
	}
	sort.Strings(aliases)
	return filename, slicesCompact(aliases)
}

func safeCoverageIdentity(value string) bool {
	return value != "" && value != "." && value != ".." && !isPortableAbs(value) && !strings.HasPrefix(value, "../") && !strings.Contains(value, "/../")
}

func portableRelative(root, candidate string) (string, bool) {
	if candidate == root {
		return ".", true
	}
	prefix := root + "/"
	if strings.HasPrefix(candidate, prefix) {
		return strings.TrimPrefix(candidate, prefix), true
	}
	return "", false
}

func isPortableAbs(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		(len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '/')
}

func parseGoCoverage(content string) (coverageData, error) {
	result := coverageData{files: make(map[string][]coverageSpan), loaded: true, format: "go-coverprofile"}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for index, raw := range lines {
		line := strings.TrimSpace(raw)
		if index == 0 && strings.HasPrefix(line, "mode:") {
			continue
		}
		match := goCoverageLine.FindStringSubmatch(line)
		if match == nil {
			return coverageData{}, fmt.Errorf("parse Go coverprofile line %d: %q", index+1, line)
		}
		start, _ := strconv.Atoi(match[2])
		end, _ := strconv.Atoi(match[3])
		statements, _ := strconv.Atoi(match[4])
		count, _ := strconv.Atoi(match[5])
		filename := normalizePortablePath(match[1])
		result.files[filename] = append(result.files[filename], coverageSpan{
			StartLine: start, EndLine: end, Statements: statements, Covered: count > 0,
		})
	}
	if len(result.files) == 0 {
		return coverageData{}, fmt.Errorf("Go coverprofile contains no coverage blocks")
	}
	return result, nil
}

func (coverage coverageData) forFile(file string) coverageMatch {
	return coverage.forFileExcluding(file, nil)
}

func (coverage coverageData) forFileExcluding(file string, excluded map[string]bool) coverageMatch {
	if !coverage.loaded {
		return coverageMatch{kind: "absent"}
	}
	normalized := normalizePortablePath(file)
	exact := make([]string, 0, 2)
	if coverage.files[normalized] != nil && !excluded[normalized] {
		exact = append(exact, normalized)
	}
	for _, canonical := range coverage.aliases[normalized] {
		if !excluded[canonical] {
			exact = appendUnique(exact, canonical)
		}
	}
	if len(exact) == 1 {
		return coverageMatch{spans: coverage.files[exact[0]], kind: "exact", identities: exact}
	}
	if len(exact) > 1 {
		sort.Strings(exact)
		return coverageMatch{kind: "ambiguous", candidates: exact}
	}
	candidates, canonical := matchingCoveragePaths(coverage, normalized, false, excluded)
	kind := "suffix"
	if len(candidates) == 0 {
		candidates, canonical = matchingCoveragePaths(coverage, normalized, true, excluded)
		kind = "case-folded"
	}
	if len(candidates) == 1 {
		return coverageMatch{spans: coverage.files[canonical[0]], kind: kind, candidates: candidates, identities: canonical}
	}
	if len(candidates) > 1 {
		return coverageMatch{kind: "ambiguous", candidates: candidates}
	}
	return coverageMatch{kind: "unmatched"}
}

func (coverage coverageData) matchFiles(files []string) []coverageMatch {
	matches := make([]coverageMatch, len(files))
	if !coverage.loaded {
		for index := range matches {
			matches[index].kind = "absent"
		}
		return matches
	}
	assigned := make([]bool, len(files))
	claimed := make(map[string]bool)
	for rank := 3; rank >= 1; rank-- {
		for {
			candidates := make(map[int][]string)
			claimants := make(map[string][]int)
			for index, file := range files {
				if assigned[index] {
					continue
				}
				for _, identity := range coverage.matchingIdentities(file, rank) {
					if !claimed[identity] {
						candidates[index] = append(candidates[index], identity)
						claimants[identity] = append(claimants[identity], index)
					}
				}
			}
			allocations := make(map[int]string)
			for identity, indexes := range claimants {
				only := -1
				for _, index := range indexes {
					if len(candidates[index]) != 1 {
						continue
					}
					if only != -1 {
						only = -2
						break
					}
					only = index
				}
				if only >= 0 {
					allocations[only] = identity
				}
			}
			if len(allocations) == 0 {
				for index, identities := range candidates {
					if len(identities) == 0 {
						continue
					}
					sort.Strings(identities)
					matches[index] = coverageMatch{kind: "ambiguous", candidates: append([]string(nil), identities...)}
					assigned[index] = true
				}
				break
			}
			for index, identity := range allocations {
				claimed[identity] = true
				assigned[index] = true
				matches[index] = coverageMatch{spans: coverage.files[identity], kind: coverageMatchKind(rank), identities: []string{identity}}
			}
		}
	}
	for index := range matches {
		if !assigned[index] {
			matches[index].kind = "unmatched"
		}
	}
	return matches
}

func (coverage coverageData) matchingIdentities(file string, rank int) []string {
	target := normalizePortablePath(file)
	identities := make(map[string]bool)
	for identity := range coverage.files {
		if pathMatchRank(identity, target) == rank {
			identities[identity] = true
		}
	}
	for alias, canonicals := range coverage.aliases {
		if pathMatchRank(alias, target) != rank {
			continue
		}
		for _, canonical := range canonicals {
			identities[canonical] = true
		}
	}
	result := make([]string, 0, len(identities))
	for identity := range identities {
		result = append(result, identity)
	}
	sort.Strings(result)
	return result
}

func pathMatchRank(candidate, target string) int {
	if candidate == target {
		return 3
	}
	if strings.HasSuffix(candidate, "/"+target) || strings.HasSuffix(target, "/"+candidate) {
		return 2
	}
	candidate, target = strings.ToLower(candidate), strings.ToLower(target)
	if candidate == target || strings.HasSuffix(candidate, "/"+target) || strings.HasSuffix(target, "/"+candidate) {
		return 1
	}
	return 0
}

func coverageMatchKind(rank int) string {
	return map[int]string{3: "exact", 2: "suffix", 1: "case-folded"}[rank]
}

func matchingCoveragePaths(coverage coverageData, target string, fold bool, excluded map[string]bool) ([]string, []string) {
	identities := make(map[string]string)
	for candidate := range coverage.files {
		identities[candidate] = candidate
	}
	for alias, canonicals := range coverage.aliases {
		for _, canonical := range canonicals {
			identities[alias+"\x00"+canonical] = canonical
		}
	}
	matches := make(map[string]string)
	for identity, canonical := range identities {
		if excluded[canonical] {
			continue
		}
		candidate, _, _ := strings.Cut(identity, "\x00")
		left, right := candidate, target
		if fold {
			left, right = strings.ToLower(left), strings.ToLower(right)
		}
		if left == right || strings.HasSuffix(left, "/"+right) || strings.HasSuffix(right, "/"+left) {
			if previous, exists := matches[canonical]; !exists || candidate < previous {
				matches[canonical] = candidate
			}
		}
	}
	candidates := make([]string, 0, len(matches))
	canonicals := make([]string, 0, len(matches))
	for canonical, candidate := range matches {
		candidates = append(candidates, candidate+"\x00"+canonical)
	}
	sort.Strings(candidates)
	for index, value := range candidates {
		candidate, canonical, _ := strings.Cut(value, "\x00")
		candidates[index] = candidate
		canonicals = append(canonicals, canonical)
	}
	return candidates, canonicals
}

func methodCoverage(spans []coverageSpan, owned []lineRange) *float64 {
	if spans == nil {
		return nil
	}
	total, covered := 0, 0
	for _, span := range spans {
		if !rangesContain(owned, span.StartLine) {
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

func rangesContain(ranges []lineRange, line int) bool {
	for _, current := range ranges {
		if line >= current.Start && line <= current.End {
			return true
		}
	}
	return false
}

func normalizePortablePath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	value = path.Clean(value)
	return strings.TrimPrefix(value, "./")
}

func slicesCompact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
