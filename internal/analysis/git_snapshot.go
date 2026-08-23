package analysis

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/hbaldwin98/crap/internal/reportcontract"
)

type gitSourceFile struct {
	path string
	data []byte
}

type gitSourceSnapshot struct {
	files     []gitSourceFile
	discovery discoveryResult
}

type baselineSourceSelector struct {
	prefix        string
	selectors     []string
	explicitFiles map[string]bool
	excludes      gitignore.Matcher
	gitIgnore     gitignore.Matcher
	crapIgnore    gitignore.Matcher
	options       Options
}

type discoveryRecorder struct {
	result *discoveryResult
	seen   map[string]map[string]bool
}

type gitTreeEntry struct {
	mode string
	typ  string
	oid  string
	path string
}

func (analyzer *Analyzer) readGitSourceSnapshot(ctx context.Context, root, repositoryRoot, commit string, options Options) (gitSourceSnapshot, error) {
	output, err := analyzer.git.Output(ctx, repositoryRoot, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return gitSourceSnapshot{}, fmt.Errorf("list baseline Git tree %s: %w", commit, err)
	}
	entries, err := parseGitTree(output)
	if err != nil {
		return gitSourceSnapshot{}, err
	}
	prefix, err := baselineRepositoryPrefix(root, repositoryRoot)
	if err != nil {
		return gitSourceSnapshot{}, err
	}
	entryByPath := make(map[string]gitTreeEntry, len(entries))
	for _, entry := range entries {
		entryByPath[entry.path] = entry
	}
	selector, err := analyzer.newBaselineSourceSelector(ctx, root, repositoryRoot, prefix, entries, entryByPath, options)
	if err != nil {
		return gitSourceSnapshot{}, err
	}
	result := gitSourceSnapshot{files: make([]gitSourceFile, 0), discovery: discoveryResult{files: make([]string, 0), exclusions: make(map[string]int), examples: make(map[string][]string)}}
	if err := analyzer.appendBaselineSourceFiles(ctx, repositoryRoot, entries, selector, &result); err != nil {
		return gitSourceSnapshot{}, err
	}
	sort.Slice(result.files, func(i, j int) bool { return result.files[i].path < result.files[j].path })
	sort.Strings(result.discovery.files)
	return result, nil
}

func baselineRepositoryPrefix(root, repositoryRoot string) (string, error) {
	repositoryPrefix, err := filepath.Rel(repositoryRoot, root)
	if err != nil || repositoryPrefix == ".." || strings.HasPrefix(repositoryPrefix, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("analysis root is outside Git repository")
	}
	prefix := normalizePath(repositoryPrefix)
	if prefix == "." {
		prefix = ""
	}
	return prefix, nil
}

func (analyzer *Analyzer) appendBaselineSourceFiles(ctx context.Context, repositoryRoot string, entries []gitTreeEntry, selector baselineSourceSelector, result *gitSourceSnapshot) error {
	recorder := discoveryRecorder{result: &result.discovery, seen: make(map[string]map[string]bool)}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, reason, selected := selector.classify(entry)
		if !selected {
			continue
		}
		if reason != "" {
			recorder.record(reason, relative)
			continue
		}
		data, err := analyzer.git.Output(ctx, repositoryRoot, "cat-file", "blob", entry.oid)
		if err != nil {
			return fmt.Errorf("read baseline source %s: %w", relative, err)
		}
		result.files = append(result.files, gitSourceFile{path: relative, data: data})
		result.discovery.files = append(result.discovery.files, relative)
	}
	return nil
}

func (analyzer *Analyzer) newBaselineSourceSelector(ctx context.Context, root, repositoryRoot, prefix string, entries []gitTreeEntry, entryByPath map[string]gitTreeEntry, options Options) (baselineSourceSelector, error) {
	selectors, explicitFiles, err := comparisonSelectors(root, options.Paths)
	if err != nil {
		return baselineSourceSelector{}, err
	}
	demoteBaselineDirectorySelectors(prefix, entries, explicitFiles)
	excludes, err := comparisonExcludeMatcher(options.Exclude)
	if err != nil {
		return baselineSourceSelector{}, err
	}
	gitMatcher, crapMatcher, err := analyzer.baselineIgnoreMatchers(ctx, repositoryRoot, prefix, entries, entryByPath)
	if err != nil {
		return baselineSourceSelector{}, err
	}
	return baselineSourceSelector{prefix: prefix, selectors: selectors, explicitFiles: explicitFiles, excludes: excludes, gitIgnore: gitMatcher, crapIgnore: crapMatcher, options: options}, nil
}

func demoteBaselineDirectorySelectors(prefix string, entries []gitTreeEntry, explicitFiles map[string]bool) {
	for path := range explicitFiles {
		repositoryPath := path
		if prefix != "" {
			repositoryPath = prefix + "/" + path
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.path, repositoryPath+"/") {
				delete(explicitFiles, path)
				break
			}
		}
	}
}

func (selector baselineSourceSelector) classify(entry gitTreeEntry) (string, string, bool) {
	relative, belowRoot := pathBelowPrefix(entry.path, selector.prefix)
	if !belowRoot || !supportedSource(relative) {
		return "", "", false
	}
	explicit, selected := selectedComparisonPath(relative, selector.selectors, selector.explicitFiles)
	if !selected {
		return "", "", false
	}
	components := strings.Split(relative, "/")
	if selector.excludes != nil && selector.excludes.Match(components, false) {
		return relative, "explicit", true
	}
	if !explicit {
		if reason := selector.ignoredReason(entry.path, relative, components); reason != "" {
			return relative, reason, true
		}
	}
	if entry.typ != "blob" || (entry.mode != "100644" && entry.mode != "100755") {
		return relative, "git-mode", true
	}
	return relative, "", true
}

func (selector baselineSourceSelector) ignoredReason(repositoryPath, relative string, components []string) string {
	if reason := selector.matcherIgnoreReason(repositoryPath, relative, components); reason != "" {
		return reason
	}
	return selector.sourceKindIgnoreReason(relative)
}

func (selector baselineSourceSelector) matcherIgnoreReason(repositoryPath, relative string, components []string) string {
	if hasBuiltInIgnoredDirectory(relative) {
		return "built-in"
	}
	if selector.crapIgnore != nil && selector.crapIgnore.Match(components, false) {
		return "crapignore"
	}
	if selector.gitIgnore != nil && selector.gitIgnore.Match(strings.Split(repositoryPath, "/"), false) {
		return "gitignore"
	}
	return ""
}

func (selector baselineSourceSelector) sourceKindIgnoreReason(relative string) string {
	if !selector.options.IncludeTests && isTestSource(relative) {
		return "test"
	}
	if !selector.options.IncludeGenerated && isGeneratedSource(relative) {
		return "generated"
	}
	return ""
}

func (recorder discoveryRecorder) record(reason, path string) {
	if recorder.seen[reason] == nil {
		recorder.seen[reason] = make(map[string]bool)
	}
	if recorder.seen[reason][path] {
		return
	}
	recorder.seen[reason][path] = true
	recorder.result.exclusions[reason]++
	examples := append(recorder.result.examples[reason], path)
	sort.Strings(examples)
	if len(examples) > discoveryExampleLimit {
		examples = examples[:discoveryExampleLimit]
	}
	recorder.result.examples[reason] = examples
}

func (analyzer *Analyzer) analyzeGitSnapshot(ctx context.Context, root string, snapshot gitSourceSnapshot, coverage coverageData, coverageMetadata CoverageMetadata, options Options) (Report, error) {
	reportOptions := options
	reportOptions.Root, reportOptions.CoveragePath, reportOptions.DiffBase = root, "", ""
	report := newAnalysisReport(reportOptions, snapshot.discovery, coverage, changedFiles{})
	report.Coverage = coverageMetadata
	files := make([]string, len(snapshot.files))
	contents := make([][]byte, len(snapshot.files))
	usedGrammars := make(map[string]bool)
	report.Fingerprints.Sources = make([]reportcontract.FileFingerprint, 0, len(snapshot.files))
	for index, file := range snapshot.files {
		files[index], contents[index] = file.path, file.data
		report.Fingerprints.Sources = append(report.Fingerprints.Sources, reportcontract.FileFingerprint{Path: file.path, SHA256: reportcontract.SHA256(file.data)})
		usedGrammars[strings.ToLower(filepath.Ext(file.path))] = true
	}
	reportcontract.SortFiles(report.Fingerprints.Sources)
	configureAnalysisReport(&report, root, reportOptions, coverage, changedFiles{}, usedGrammars, analyzer.languages)
	if coverage.loaded {
		report.Fingerprints.Coverage = &reportcontract.FileFingerprint{Path: normalizeDisplayedPath(coverageMetadata.DisplayedPath), SHA256: coverage.sha256}
	}
	matches, err := coverage.matchFilesContext(ctx, files)
	if err != nil {
		return Report{}, err
	}
	results, err := analyzer.analyzeFiles(ctx, files, files, contents, matches, changedFiles{}, reportOptions)
	if err != nil {
		return Report{}, err
	}
	if err := appendAnalysisResults(ctx, &report, results, options.StrictCoverage); err != nil {
		return Report{}, err
	}
	if err := finalizeAnalysisReport(ctx, &report, len(files)); err != nil {
		return Report{}, err
	}
	return report, nil
}

func parseGitTree(data []byte) ([]gitTreeEntry, error) {
	records := bytes.Split(bytes.TrimSuffix(data, []byte{0}), []byte{0})
	entries := make([]gitTreeEntry, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		header, filename, ok := bytes.Cut(record, []byte{'\t'})
		if !ok {
			return nil, fmt.Errorf("parse Git tree entry %q", record)
		}
		if bytes.Contains(filename, []byte{'\\'}) {
			return nil, fmt.Errorf("baseline Git path %q contains a backslash unsupported by the report path contract", filename)
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || len(filename) == 0 {
			return nil, fmt.Errorf("parse Git tree entry %q", record)
		}
		entries = append(entries, gitTreeEntry{mode: fields[0], typ: fields[1], oid: fields[2], path: strings.TrimPrefix(filepath.ToSlash(filepath.Clean(string(filename))), "./")})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, nil
}

func (analyzer *Analyzer) baselineIgnoreMatchers(ctx context.Context, repositoryRoot, analysisPrefix string, entries []gitTreeEntry, byPath map[string]gitTreeEntry) (gitignore.Matcher, gitignore.Matcher, error) {
	patterns, err := analyzer.baselineGitignorePatterns(ctx, repositoryRoot, entries)
	if err != nil {
		return nil, nil, err
	}
	var gitMatcher gitignore.Matcher
	if len(patterns) > 0 {
		gitMatcher = gitignore.NewMatcher(patterns)
	}
	crapPath := ".crapignore"
	if analysisPrefix != "" {
		crapPath = analysisPrefix + "/.crapignore"
	}
	var crapMatcher gitignore.Matcher
	if entry, ok := byPath[crapPath]; ok && entry.typ == "blob" && (entry.mode == "100644" || entry.mode == "100755") {
		data, err := analyzer.readBaselineBlob(ctx, repositoryRoot, entry, "baseline .crapignore")
		if err != nil {
			return nil, nil, err
		}
		if crapPatterns := parseIgnoreData(data, nil); len(crapPatterns) > 0 {
			crapMatcher = gitignore.NewMatcher(crapPatterns)
		}
	}
	return gitMatcher, crapMatcher, nil
}

func (analyzer *Analyzer) baselineGitignorePatterns(ctx context.Context, repositoryRoot string, entries []gitTreeEntry) ([]gitignore.Pattern, error) {
	ignoreEntries := make([]gitTreeEntry, 0)
	for _, entry := range entries {
		if filepath.Base(filepath.FromSlash(entry.path)) == ".gitignore" && entry.typ == "blob" && (entry.mode == "100644" || entry.mode == "100755") {
			ignoreEntries = append(ignoreEntries, entry)
		}
	}
	sort.Slice(ignoreEntries, func(i, j int) bool {
		leftDepth, rightDepth := strings.Count(ignoreEntries[i].path, "/"), strings.Count(ignoreEntries[j].path, "/")
		if leftDepth == rightDepth {
			return ignoreEntries[i].path < ignoreEntries[j].path
		}
		return leftDepth < rightDepth
	})
	patterns := make([]gitignore.Pattern, 0)
	for _, entry := range ignoreEntries {
		data, err := analyzer.readBaselineBlob(ctx, repositoryRoot, entry, "baseline .gitignore "+entry.path)
		if err != nil {
			return nil, err
		}
		directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(entry.path)))
		var domain []string
		if directory != "." {
			domain = strings.Split(directory, "/")
		}
		patterns = append(patterns, parseIgnoreData(data, domain)...)
	}
	return patterns, nil
}

func (analyzer *Analyzer) readBaselineBlob(ctx context.Context, repositoryRoot string, entry gitTreeEntry, label string) ([]byte, error) {
	data, err := analyzer.git.Output(ctx, repositoryRoot, "cat-file", "blob", entry.oid)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	return data, nil
}

func parseIgnoreData(data []byte, domain []string) []gitignore.Pattern {
	patterns := make([]gitignore.Pattern, 0)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(portableIgnoreEscapes(line), domain))
	}
	return patterns
}

func comparisonSelectors(root string, paths []string) ([]string, map[string]bool, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	selectors := make([]string, 0, len(paths))
	explicit := make(map[string]bool)
	for _, value := range paths {
		path := value
		if filepath.IsAbs(path) {
			var err error
			path, err = filepath.Rel(root, path)
			if err != nil {
				return nil, nil, fmt.Errorf("make comparison path relative: %w", err)
			}
		}
		path = normalizePath(path)
		if path == ".." || strings.HasPrefix(path, "../") {
			return nil, nil, fmt.Errorf("comparison path %s is outside analysis root", value)
		}
		if path == "." {
			path = ""
		}
		selectors = append(selectors, path)
		if path != "" && supportedSource(path) {
			explicit[path] = true
		}
	}
	sort.Strings(selectors)
	return selectors, explicit, nil
}

func comparisonExcludeMatcher(excludes []string) (gitignore.Matcher, error) {
	patterns := make([]gitignore.Pattern, 0, len(excludes))
	for _, pattern := range excludes {
		if strings.TrimSpace(pattern) == "" || strings.HasPrefix(pattern, "!") {
			return nil, fmt.Errorf("exclude pattern %q must select a path and cannot be negated", pattern)
		}
		patterns = append(patterns, gitignore.ParsePattern(portableIgnoreEscapes(pattern), nil))
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	return gitignore.NewMatcher(patterns), nil
}

func pathBelowPrefix(path, prefix string) (string, bool) {
	if prefix == "" {
		return path, true
	}
	if path == prefix {
		return "", true
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return "", false
	}
	return strings.TrimPrefix(path, prefix+"/"), true
}

func selectedComparisonPath(path string, selectors []string, explicit map[string]bool) (bool, bool) {
	if explicit[path] {
		return true, true
	}
	for _, selector := range selectors {
		if !explicit[selector] && (selector == "" || path == selector || strings.HasPrefix(path, selector+"/")) {
			return false, true
		}
	}
	return false, false
}

func hasBuiltInIgnoredDirectory(path string) bool {
	parts := strings.Split(path, "/")
	for index := 1; index < len(parts); index++ {
		if builtInIgnoredDirectory(strings.Join(parts[:index], "/"), true) {
			return true
		}
	}
	return false
}
