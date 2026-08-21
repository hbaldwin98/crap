package analysis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
	treesitter "github.com/tree-sitter/go-tree-sitter"
)

type languageDefinition struct {
	name            string
	grammarLanguage string
	grammarVersion  string
	parser          *treesitter.Parser
	callableKinds   map[string]string
	branchKinds     map[string]bool
	logicalOps      map[string]bool
	callable        func(*treesitter.Node) bool
	body            func(*treesitter.Node) *treesitter.Node
	extraBranch     func(*treesitter.Node, []byte) bool
	qualifiedName   func(*treesitter.Node, []byte) string
	ownerName       func(*treesitter.Node, []byte) string
}

func (language languageDefinition) callableKind(node *treesitter.Node) (string, bool) {
	kind, ok := language.callableKinds[node.Kind()]
	return kind, ok && (language.callable == nil || language.callable(node))
}

func (language languageDefinition) callableBody(node *treesitter.Node) *treesitter.Node {
	if language.body != nil {
		return language.body(node)
	}
	return node.ChildByFieldName("body")
}

func (language languageDefinition) isBranch(node *treesitter.Node, source []byte) bool {
	return language.branchKinds[node.Kind()] || (language.extraBranch != nil && language.extraBranch(node, source))
}

type Analyzer struct {
	languages map[string]languageDefinition
	git       gitRunner
}

func NewAnalyzer() (*Analyzer, error) {
	languages := make(map[string]languageDefinition, 4)
	definitions := []struct {
		extension string
		load      func() (languageDefinition, error)
	}{
		{".cs", newCSharpLanguage},
		{".go", newGoLanguage},
		{".ts", func() (languageDefinition, error) { return newTypeScriptLanguage(false) }},
		{".tsx", func() (languageDefinition, error) { return newTypeScriptLanguage(true) }},
	}
	for _, definition := range definitions {
		language, err := definition.load()
		if err != nil {
			for _, loaded := range languages {
				loaded.parser.Close()
			}
			return nil, err
		}
		languages[definition.extension] = language
	}
	return &Analyzer{languages: languages, git: execGitRunner{}}, nil
}

func (analyzer *Analyzer) Close() {
	for _, language := range analyzer.languages {
		language.parser.Close()
	}
}

func (analyzer *Analyzer) Analyze(options Options) (Report, error) {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return Report{}, fmt.Errorf("resolve root: %w", err)
	}
	if options.Authorization != nil {
		root = options.Authorization.Path()
	} else {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return Report{}, fmt.Errorf("resolve root links: %w", err)
		}
	}
	files, err := findSourceFiles(root, options.Paths, options.IncludeTests, options.Authorization)
	if err != nil {
		return Report{}, err
	}
	coveragePath := options.CoveragePath
	if coveragePath != "" && !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(root, coveragePath)
	}
	if coveragePath != "" && options.Authorization != nil {
		coveragePath, err = options.Authorization.Existing(coveragePath)
		if err != nil {
			return Report{}, fmt.Errorf("authorize coverage report: %w", err)
		}
	}
	coverage, err := loadCoverage(coveragePath, root)
	if err != nil {
		return Report{}, err
	}
	changes, err := gitChangedLines(root, options.DiffBase, files, analyzer.git, options.Authorization)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		SchemaVersion:  SchemaVersion,
		ReportType:     "analysis",
		Tool:           buildinfo.Tool("crap"),
		Coordinates:    reportcontract.DefaultCoordinates(),
		Mode:           "all",
		Coverage:       CoverageMetadata{Format: "none"},
		DiffBase:       options.DiffBase,
		DiffBaseCommit: changes.BaseCommit,
		DiffHeadCommit: changes.HeadCommit,
		DiffMergeBase:  changes.MergeBase,
		Threshold:      options.CRAPThreshold,
		Methods:        make([]MethodResult, 0),
		Diagnostics:    make([]Diagnostic, 0),
		Grammars:       make([]GrammarIdentity, 0),
	}
	if coverage.loaded {
		report.Coverage = CoverageMetadata{Format: coverage.format, DisplayedPath: normalizeDisplayedPath(options.CoveragePath), SHA256: coverage.sha256}
	}
	if options.DiffBase != "" {
		report.Mode = "changed"
	}
	relativeFiles := make([]string, len(files))
	fileContents := make([][]byte, len(files))
	report.Fingerprints.Sources = make([]reportcontract.FileFingerprint, 0, len(files))
	usedGrammars := make(map[string]bool)
	for index, file := range files {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return Report{}, fmt.Errorf("make path relative: %w", err)
		}
		relativeFiles[index] = normalizePath(relative)
		data, err := os.ReadFile(file)
		if err != nil {
			return Report{}, fmt.Errorf("fingerprint %s: %w", file, err)
		}
		fileContents[index] = data
		report.Fingerprints.Sources = append(report.Fingerprints.Sources, reportcontract.FileFingerprint{Path: relativeFiles[index], SHA256: reportcontract.SHA256(data)})
		usedGrammars[strings.ToLower(filepath.Ext(file))] = true
	}
	reportcontract.SortFiles(report.Fingerprints.Sources)
	if coverage.loaded {
		report.Fingerprints.Coverage = &reportcontract.FileFingerprint{Path: normalizeDisplayedPath(options.CoveragePath), SHA256: coverage.sha256}
	}
	if changes.BaseCommit != "" || changes.HeadCommit != "" || changes.MergeBase != "" {
		report.Fingerprints.Git = &reportcontract.GitFingerprint{BaseCommit: changes.BaseCommit, HeadCommit: changes.HeadCommit, MergeBase: changes.MergeBase}
	}
	configPaths := append(make([]string, 0, len(options.Paths)), options.Paths...)
	if len(configPaths) == 0 {
		configPaths = []string{"."}
	}
	for index := range configPaths {
		configPaths[index] = semanticPath(root, configPaths[index])
	}
	sort.Strings(configPaths)
	report.Fingerprints.ConfigSHA256 = reportcontract.JSONFingerprint(struct {
		Paths          []string `json:"paths"`
		DiffBase       string   `json:"diffBase"`
		Threshold      float64  `json:"threshold"`
		IncludeTests   bool     `json:"includeTests"`
		StrictCoverage bool     `json:"strictCoverage"`
	}{configPaths, options.DiffBase, options.CRAPThreshold, options.IncludeTests, options.StrictCoverage})
	for extension := range usedGrammars {
		language := analyzer.languages[extension]
		report.Grammars = append(report.Grammars, GrammarIdentity{Language: language.grammarLanguage, Version: language.grammarVersion})
	}
	sort.Slice(report.Grammars, func(i, j int) bool { return report.Grammars[i].Language < report.Grammars[j].Language })
	coverageMatches := coverage.matchFiles(relativeFiles)

	for index, file := range files {
		methods, diagnostic, err := analyzer.analyzeFile(file, relativeFiles[index], fileContents[index], coverageMatches[index], changes, options)
		if err != nil {
			return Report{}, err
		}
		report.Methods = append(report.Methods, methods...)
		if diagnostic != nil {
			report.Diagnostics = append(report.Diagnostics, *diagnostic)
			if options.StrictCoverage && (diagnostic.Code == "coverage-path-unmatched" || diagnostic.Code == "coverage-path-ambiguous") {
				return Report{}, fmt.Errorf("strict coverage: %s: %s", diagnostic.Path, diagnostic.Message)
			}
		}
	}
	sort.Slice(report.Methods, func(i, j int) bool {
		if report.Methods[i].File == report.Methods[j].File {
			return report.Methods[i].StartLine < report.Methods[j].StartLine
		}
		return report.Methods[i].File < report.Methods[j].File
	})
	report.Summary = summarize(report.Methods)
	report.Summary.Files = len(files)
	sort.Slice(report.Diagnostics, func(i, j int) bool {
		if report.Diagnostics[i].Code != report.Diagnostics[j].Code {
			return report.Diagnostics[i].Code < report.Diagnostics[j].Code
		}
		return report.Diagnostics[i].Path < report.Diagnostics[j].Path
	})
	return report, nil
}

func (analyzer *Analyzer) analyzeFile(path, relative string, source []byte, match coverageMatch, changes changedFiles, options Options) ([]MethodResult, *Diagnostic, error) {
	language, ok := analyzer.languages[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported source file %s", path)
	}
	tree := language.parser.Parse(source, nil)
	if tree == nil {
		return nil, nil, fmt.Errorf("parse %s: parser returned no tree", path)
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil, nil, fmt.Errorf("parse %s: source contains syntax errors", path)
	}

	callables := make([]callableNode, 0)
	collectCallableNodes(tree.RootNode(), language, 0, &callables)
	results := make([]MethodResult, 0, len(callables))
	occurrences := make(map[string]int)
	for index, callable := range callables {
		signature := lexicalSignature(callable.node, source, language)
		identity := callableIdentity(callable.node, source, language)
		owner := language.qualifiedName(callable.node, source)
		ownerIdentity := owner
		if strings.Contains(ownerIdentity, "<anonymous@") {
			ownerIdentity = "anonymous"
		}
		stableOwner := ownerIdentity + "\x00" + callableOwnerIdentity(callable.node, source, language)
		occurrenceKey := callable.kind + "\x00" + identity + "\x00" + stableOwner
		occurrence := occurrences[occurrenceKey]
		occurrences[occurrenceKey]++
		result := resultForNode(callable.node, source, path, relative, callable.kind, owner, signature, identity, stableOwner, occurrence, match.spans, callableOwnedRanges(callables, index), changes, options.CRAPThreshold, language)
		if options.DiffBase == "" || result.Changed {
			results = append(results, result)
		}
	}
	return results, coverageDiagnostic(relative, match), nil
}

func coverageDiagnostic(file string, match coverageMatch) *Diagnostic {
	var diagnostic Diagnostic
	switch match.kind {
	case "suffix":
		diagnostic = Diagnostic{Severity: "warning", Code: "coverage-path-suffix-matched", Message: "coverage path matched by a unique component suffix", Path: file}
	case "case-folded":
		diagnostic = Diagnostic{Severity: "warning", Code: "coverage-path-case-folded", Message: "coverage path matched case-insensitively", Path: file}
	case "unmatched":
		diagnostic = Diagnostic{Severity: "error", Code: "coverage-path-unmatched", Message: "no coverage entry matched this analyzed source file", Path: file}
	case "ambiguous":
		diagnostic = Diagnostic{Severity: "error", Code: "coverage-path-ambiguous", Message: "coverage attribution is ambiguous across report entries or analyzed source files", Path: file, Candidates: append([]string(nil), match.candidates...)}
	default:
		return nil
	}
	return &diagnostic
}

type callableNode struct {
	node  *treesitter.Node
	kind  string
	depth int
}

func collectCallableNodes(node *treesitter.Node, language languageDefinition, depth int, results *[]callableNode) {
	if kind, ok := language.callableKind(node); ok {
		*results = append(*results, callableNode{node: node, kind: kind, depth: depth})
		depth++
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		collectCallableNodes(node.NamedChild(index), language, depth, results)
	}
}

func resultForNode(node *treesitter.Node, source []byte, sourcePath, file, kind, name, signature, identity, stableOwner string, occurrence int, coverage []coverageSpan, owned []lineRange, changes changedFiles, threshold float64, language languageDefinition) MethodResult {
	start := int(node.StartPosition().Row) + 1
	end := int(node.EndPosition().Row) + 1
	complexity := complexity(node, source, language)
	covered := methodCoverage(coverage, owned)
	coverageForScore := 0.0
	if covered != nil {
		coverageForScore = *covered
	}
	score := CRAPScore(complexity, coverageForScore)
	return MethodResult{
		ID:              reportcontract.Fingerprint(language.name, file, kind, stableOwner, identity, strconv.Itoa(occurrence)),
		Language:        language.name,
		File:            file,
		Name:            name,
		Kind:            kind,
		Signature:       signature,
		StartLine:       start,
		StartColumn:     int(node.StartPosition().Column) + 1,
		EndLine:         end,
		EndColumn:       int(node.EndPosition().Column) + 1,
		Complexity:      complexity,
		CoveragePercent: covered,
		CRAP:            score,
		Changed:         changes.intersects(sourcePath, start, end),
		AboveThreshold:  score > threshold,
	}
}

func lexicalSignature(node *treesitter.Node, source []byte, language languageDefinition) string {
	end := node.EndByte()
	if body := language.callableBody(node); body != nil {
		end = body.StartByte()
	}
	return strings.Join(strings.Fields(string(source[node.StartByte():end])), " ")
}

func callableIdentity(node *treesitter.Node, source []byte, language languageDefinition) string {
	return callableStructuralIdentity(node, source, language.callableBody(node))
}

func callableStructuralIdentity(node *treesitter.Node, source []byte, body *treesitter.Node) string {
	var identity strings.Builder
	var visit func(*treesitter.Node)
	visit = func(current *treesitter.Node) {
		if body != nil && current.StartByte() == body.StartByte() && current.EndByte() == body.EndByte() {
			return
		}
		if strings.Contains(current.Kind(), "comment") {
			return
		}
		if current.ChildCount() == 0 {
			identity.WriteString(current.Kind())
			identity.WriteByte(':')
			identity.Write(source[current.StartByte():current.EndByte()])
			identity.WriteByte(0)
			return
		}
		for index := uint(0); index < current.ChildCount(); index++ {
			visit(current.Child(index))
		}
	}
	visit(node)
	return identity.String()
}

func callableOwnerIdentity(node *treesitter.Node, source []byte, language languageDefinition) string {
	parts := make([]string, 0)
	seen := make(map[string]bool)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			parts = append(parts, value)
		}
	}
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if _, ok := language.callableKind(parent); ok {
			name := language.qualifiedName(parent, source)
			if strings.Contains(name, "<anonymous@") {
				name = "anonymous"
			}
			add("callable:" + name + ":" + callableIdentity(parent, source, language))
		}
		if language.ownerName != nil {
			add(language.ownerName(parent, source))
		}
	}
	if len(parts) == 0 {
		return "anonymous"
	}
	return strings.Join(parts, "\x00")
}

func nodeSource(node *treesitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return string(source[node.StartByte():node.EndByte()])
}

func callableOwnedRanges(callables []callableNode, target int) []lineRange {
	node := callables[target].node
	owned := []lineRange{{Start: int(node.StartPosition().Row) + 1, End: int(node.EndPosition().Row) + 1}}
	excluded := make([]lineRange, 0)
	for index, candidate := range callables {
		if index == target || !callablePrecedes(candidate, callables[target]) {
			continue
		}
		excluded = append(excluded, lineRange{Start: int(candidate.node.StartPosition().Row) + 1, End: int(candidate.node.EndPosition().Row) + 1})
	}
	for _, excluded := range mergeRanges(excluded) {
		next := make([]lineRange, 0, len(owned)+1)
		for _, current := range owned {
			if excluded.End < current.Start || excluded.Start > current.End {
				next = append(next, current)
				continue
			}
			if excluded.Start > current.Start {
				next = append(next, lineRange{Start: current.Start, End: excluded.Start - 1})
			}
			if excluded.End < current.End {
				next = append(next, lineRange{Start: excluded.End + 1, End: current.End})
			}
		}
		owned = next
	}
	return owned
}

func callablePrecedes(candidate, target callableNode) bool {
	if candidate.depth != target.depth {
		return candidate.depth > target.depth
	}
	if candidate.node.StartByte() != target.node.StartByte() {
		return candidate.node.StartByte() < target.node.StartByte()
	}
	return candidate.node.EndByte() < target.node.EndByte()
}

func complexity(root *treesitter.Node, source []byte, language languageDefinition) int {
	value := 1
	var visit func(*treesitter.Node, bool)
	visit = func(node *treesitter.Node, isRoot bool) {
		if !isRoot {
			if _, nestedCallable := language.callableKind(node); nestedCallable {
				return
			}
		}
		if language.isBranch(node, source) {
			value++
		}
		if node.Kind() == "binary_expression" {
			operator := node.ChildByFieldName("operator")
			if operator != nil {
				if language.logicalOps[operator.Utf8Text(source)] {
					value++
				}
			}
		}
		for index := uint(0); index < node.NamedChildCount(); index++ {
			visit(node.NamedChild(index), false)
		}
	}
	visit(root, true)
	return value
}

type sourceCollector struct {
	root          string
	includeTests  bool
	seen          map[string]bool
	files         []string
	authorization *rootauth.Root
}

func findSourceFiles(root string, paths []string, includeTests bool, authorization *rootauth.Root) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	collector := sourceCollector{root: root, includeTests: includeTests, seen: make(map[string]bool), authorization: authorization}
	for _, requested := range paths {
		if err := collector.add(root, requested); err != nil {
			return nil, err
		}
	}
	sort.Strings(collector.files)
	return collector.files, nil
}

func (collector *sourceCollector) add(root, requested string) error {
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if collector.authorization != nil {
		var err error
		path, err = collector.authorization.Existing(path)
		if err != nil {
			return fmt.Errorf("authorize source %s: %w", requested, err)
		}
	} else {
		var err error
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve source %s: %w", requested, err)
		}
		if !pathWithinRoot(collector.root, path) {
			return fmt.Errorf("source %s is outside analysis root", requested)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", requested, err)
	}
	if info.IsDir() {
		if err := filepath.WalkDir(path, collector.visit); err != nil {
			return fmt.Errorf("walk %s: %w", requested, err)
		}
		return nil
	}
	return collector.collect(path)
}

func (collector *sourceCollector) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() && ignoredDirectory(entry.Name()) {
		return filepath.SkipDir
	}
	if !entry.IsDir() {
		return collector.collect(path)
	}
	return nil
}

func (collector *sourceCollector) collect(path string) error {
	if collector.authorization != nil {
		canonical, err := collector.authorization.Existing(path)
		if err != nil {
			return fmt.Errorf("authorize discovered source %s: %w", path, err)
		}
		path = canonical
	} else {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve discovered source %s: %w", path, err)
		}
		if !pathWithinRoot(collector.root, canonical) {
			return fmt.Errorf("discovered source %s is outside analysis root", path)
		}
		path = canonical
	}
	if isSourceFile(path, collector.includeTests) && !collector.seen[path] {
		collector.seen[path] = true
		collector.files = append(collector.files, path)
	}
	return nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func ignoredDirectory(name string) bool {
	return name == ".git" || name == "bin" || name == "obj" || name == "node_modules"
}

func isSourceFile(path string, includeTests bool) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".go" && !includeTests && strings.HasSuffix(strings.ToLower(path), "_test.go") {
		return false
	}
	if (extension == ".ts" || extension == ".tsx") && !includeTests && isTypeScriptTest(path) {
		return false
	}
	return extension == ".cs" || extension == ".go" || extension == ".ts" || extension == ".tsx"
}

func isTypeScriptTest(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.tsx") || strings.HasSuffix(base, ".test.tsx")
}

func summarize(methods []MethodResult) Summary {
	summary := Summary{Methods: len(methods)}
	if len(methods) == 0 {
		return summary
	}
	var complexityTotal int
	var crapTotal float64
	for _, method := range methods {
		complexityTotal += method.Complexity
		crapTotal += method.CRAP
		if method.Changed {
			summary.ChangedMethods++
		}
		if method.AboveThreshold {
			summary.AboveThreshold++
		}
		if method.CRAP > summary.MaximumCRAP {
			summary.MaximumCRAP = method.CRAP
		}
	}
	summary.AverageComplexity = round(float64(complexityTotal)/float64(len(methods)), 2)
	summary.AverageCRAP = round(crapTotal/float64(len(methods)), 2)
	return summary
}

func normalizePath(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
}

func normalizeDisplayedPath(value string) string {
	if value == "" || filepath.IsAbs(value) {
		return ""
	}
	return normalizePath(value)
}

func semanticPath(root, value string) string {
	if !filepath.IsAbs(value) {
		return normalizePath(value)
	}
	relative, err := filepath.Rel(root, value)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return normalizePath(relative)
}
