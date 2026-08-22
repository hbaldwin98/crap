package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hbaldwin98/crap/internal/buildinfo"
	"github.com/hbaldwin98/crap/internal/reportcontract"
	treesitter "github.com/tree-sitter/go-tree-sitter"
)

type languageDefinition struct {
	name            string
	grammarLanguage string
	grammarVersion  string
	grammar         *treesitter.Language
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
			return nil, err
		}
		languages[definition.extension] = language
	}
	return &Analyzer{languages: languages, git: execGitRunner{}}, nil
}

func (analyzer *Analyzer) Close() {
}

func (analyzer *Analyzer) Analyze(options Options) (Report, error) {
	return analyzer.AnalyzeContext(context.Background(), options)
}

func (analyzer *Analyzer) AnalyzeContext(ctx context.Context, options Options) (Report, error) {
	inputs, err := analyzer.prepareAnalysis(ctx, options)
	if err != nil {
		return Report{}, err
	}
	report := newAnalysisReport(options, inputs.discovery, inputs.coverage, inputs.changes)
	relativeFiles, fileContents, usedGrammars, err := fingerprintSources(ctx, inputs.root, inputs.files, &report)
	if err != nil {
		return Report{}, err
	}
	configureAnalysisReport(&report, inputs.root, options, inputs.coverage, inputs.changes, usedGrammars, analyzer.languages)
	coverageMatches, err := inputs.coverage.matchFilesContext(ctx, relativeFiles)
	if err != nil {
		return Report{}, err
	}
	results, err := analyzer.analyzeFiles(ctx, inputs.files, relativeFiles, fileContents, coverageMatches, inputs.changes, options)
	if err != nil {
		return Report{}, err
	}
	if err := appendAnalysisResults(ctx, &report, results, options.StrictCoverage); err != nil {
		return Report{}, err
	}
	if err := finalizeAnalysisReport(ctx, &report, len(inputs.files)); err != nil {
		return Report{}, err
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return report, nil
}

type analysisInputs struct {
	root      string
	discovery discoveryResult
	files     []string
	coverage  coverageData
	changes   changedFiles
}

func (analyzer *Analyzer) prepareAnalysis(ctx context.Context, options Options) (analysisInputs, error) {
	if err := ctx.Err(); err != nil {
		return analysisInputs{}, err
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return analysisInputs{}, fmt.Errorf("resolve root: %w", err)
	}
	if options.Authorization != nil {
		root = options.Authorization.Path()
	} else {
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return analysisInputs{}, fmt.Errorf("resolve root links: %w", err)
		}
	}
	discovery, err := findSourceFilesContext(ctx, root, options.Paths, options.Exclude, options.IncludeTests, options.IncludeGenerated, options.Authorization)
	if err != nil {
		return analysisInputs{}, err
	}
	files := discovery.files
	coveragePath := options.CoveragePath
	if coveragePath != "" && !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(root, coveragePath)
	}
	if coveragePath != "" && options.Authorization != nil {
		coveragePath, err = options.Authorization.Existing(coveragePath)
		if err != nil {
			return analysisInputs{}, fmt.Errorf("authorize coverage report: %w", err)
		}
	}
	coverage, err := loadCoverageContext(ctx, coveragePath, root)
	if err != nil {
		return analysisInputs{}, err
	}
	changes, err := gitChangedLinesContext(ctx, root, options.DiffBase, files, analyzer.git, options.Authorization)
	if err != nil {
		return analysisInputs{}, err
	}
	return analysisInputs{root: root, discovery: discovery, files: files, coverage: coverage, changes: changes}, nil
}

func newAnalysisReport(options Options, discovery discoveryResult, coverage coverageData, changes changedFiles) Report {
	report := Report{
		SchemaVersion:  SchemaVersion,
		ReportType:     "analysis",
		Tool:           buildinfo.Tool("crap"),
		Coordinates:    reportcontract.DefaultCoordinates(),
		Mode:           "all",
		Coverage:       CoverageMetadata{Format: "none"},
		Discovery:      discovery.metadata(),
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
	return report
}

func fingerprintSources(ctx context.Context, root string, files []string, report *Report) ([]string, [][]byte, map[string]bool, error) {
	relativeFiles := make([]string, len(files))
	fileContents := make([][]byte, len(files))
	report.Fingerprints.Sources = make([]reportcontract.FileFingerprint, 0, len(files))
	usedGrammars := make(map[string]bool)
	for index, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("make path relative: %w", err)
		}
		relativeFiles[index] = normalizePath(relative)
		data, err := os.ReadFile(file)
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, nil, nil, contextErr
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("fingerprint %s: %w", file, err)
		}
		fileContents[index] = data
		report.Fingerprints.Sources = append(report.Fingerprints.Sources, reportcontract.FileFingerprint{Path: relativeFiles[index], SHA256: reportcontract.SHA256(data)})
		usedGrammars[strings.ToLower(filepath.Ext(file))] = true
	}
	reportcontract.SortFiles(report.Fingerprints.Sources)
	return relativeFiles, fileContents, usedGrammars, nil
}

func configureAnalysisReport(report *Report, root string, options Options, coverage coverageData, changes changedFiles, usedGrammars map[string]bool, languages map[string]languageDefinition) {
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
	configExcludes := append(make([]string, 0, len(options.Exclude)), options.Exclude...)
	sort.Strings(configExcludes)
	configExcludes = slicesCompact(configExcludes)
	report.Fingerprints.ConfigSHA256 = reportcontract.JSONFingerprint(struct {
		Contract         string            `json:"contract"`
		Paths            []string          `json:"paths"`
		DiffBase         string            `json:"diffBase"`
		Threshold        float64           `json:"threshold"`
		IncludeTests     bool              `json:"includeTests"`
		IncludeGenerated bool              `json:"includeGenerated"`
		Exclude          []string          `json:"exclude"`
		StrictCoverage   bool              `json:"strictCoverage"`
		Discovery        DiscoveryMetadata `json:"discovery"`
	}{SchemaVersion, configPaths, options.DiffBase, options.CRAPThreshold, options.IncludeTests, options.IncludeGenerated, configExcludes, options.StrictCoverage, report.Discovery})
	for extension := range usedGrammars {
		language := languages[extension]
		report.Grammars = append(report.Grammars, GrammarIdentity{Language: language.grammarLanguage, Version: language.grammarVersion})
	}
	sort.Slice(report.Grammars, func(i, j int) bool { return report.Grammars[i].Language < report.Grammars[j].Language })
}

func appendAnalysisResults(ctx context.Context, report *Report, results []fileAnalysis, strictCoverage bool) error {
	for _, result := range results {
		if err := ctx.Err(); err != nil {
			return err
		}
		report.Methods = append(report.Methods, result.methods...)
		if result.diagnostic != nil {
			report.Diagnostics = append(report.Diagnostics, *result.diagnostic)
			if strictCoverage && (result.diagnostic.Code == "coverage-path-unmatched" || result.diagnostic.Code == "coverage-path-ambiguous") {
				return fmt.Errorf("strict coverage: %s: %s", result.diagnostic.Path, result.diagnostic.Message)
			}
		}
	}
	return nil
}

func finalizeAnalysisReport(ctx context.Context, report *Report, fileCount int) error {
	sort.Slice(report.Methods, func(i, j int) bool {
		if report.Methods[i].File == report.Methods[j].File {
			return report.Methods[i].StartLine < report.Methods[j].StartLine
		}
		return report.Methods[i].File < report.Methods[j].File
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	report.Summary = summarize(report.Methods)
	report.Summary.Files = fileCount
	sort.Slice(report.Diagnostics, func(i, j int) bool {
		if report.Diagnostics[i].Code != report.Diagnostics[j].Code {
			return report.Diagnostics[i].Code < report.Diagnostics[j].Code
		}
		return report.Diagnostics[i].Path < report.Diagnostics[j].Path
	})
	return ctx.Err()
}

type fileAnalysis struct {
	methods    []MethodResult
	diagnostic *Diagnostic
	err        error
}

func (analyzer *Analyzer) analyzeFiles(ctx context.Context, files, relativeFiles []string, contents [][]byte, matches []coverageMatch, changes changedFiles, options Options) ([]fileAnalysis, error) {
	results := make([]fileAnalysis, len(files))
	if len(files) == 0 {
		return results, nil
	}
	jobs := make(chan int)
	workers := min(runtime.GOMAXPROCS(0), len(files))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			parsers := make(map[string]*treesitter.Parser)
			defer func() {
				for _, parser := range parsers {
					parser.Close()
				}
			}()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				extension := strings.ToLower(filepath.Ext(files[index]))
				parser := parsers[extension]
				if parser == nil {
					parser = treesitter.NewParser()
					language := analyzer.languages[extension]
					if err := parser.SetLanguage(language.grammar); err != nil {
						parser.Close()
						results[index].err = fmt.Errorf("load %s grammar: %w", language.grammarLanguage, err)
						continue
					}
					parsers[extension] = parser
				}
				results[index].methods, results[index].diagnostic, results[index].err = analyzer.analyzeFile(ctx, parser, files[index], relativeFiles[index], contents[index], matches[index], changes, options)
			}
		}()
	}
dispatch:
	for index := range files {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index := range results {
		if results[index].err != nil {
			return nil, results[index].err
		}
	}
	return results, nil
}

func (analyzer *Analyzer) analyzeFile(ctx context.Context, parser *treesitter.Parser, path, relative string, source []byte, match coverageMatch, changes changedFiles, options Options) ([]MethodResult, *Diagnostic, error) {
	language, ok := analyzer.languages[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, nil, fmt.Errorf("unsupported source file %s", path)
	}
	// ParseWithOptions in go-tree-sitter v0.25.0 leaks its progress callback
	// payload. ParseCtx uses Tree-sitter's cancellation flag without retaining
	// request state; reset below clears that flag after cancellation.
	tree := parser.ParseCtx(ctx, source, nil)
	if err := ctx.Err(); err != nil {
		if tree != nil {
			tree.Close()
		}
		parser.Reset()
		return nil, nil, err
	}
	if tree == nil {
		parser.Reset()
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("parse %s: parser returned no tree", path)
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil, nil, fmt.Errorf("parse %s: source contains syntax errors", path)
	}

	callables := make([]callableNode, 0)
	if err := collectCallableNodes(ctx, tree.RootNode(), language, 0, &callables); err != nil {
		return nil, nil, err
	}
	results := make([]MethodResult, 0, len(callables))
	occurrences := make(map[string]int)
	for index, callable := range callables {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		signature := lexicalSignature(callable.node, source, language)
		identity, err := callableIdentityContext(ctx, callable.node, source, language)
		if err != nil {
			return nil, nil, err
		}
		owner := language.qualifiedName(callable.node, source)
		ownerIdentity := owner
		if strings.Contains(ownerIdentity, "<anonymous@") {
			ownerIdentity = "anonymous"
		}
		ownerStructure, err := callableOwnerIdentityContext(ctx, callable.node, source, language)
		if err != nil {
			return nil, nil, err
		}
		stableOwner := ownerIdentity + "\x00" + ownerStructure
		occurrenceKey := callable.kind + "\x00" + identity + "\x00" + stableOwner
		occurrence := occurrences[occurrenceKey]
		occurrences[occurrenceKey]++
		owned, err := callableOwnedRangesContext(ctx, callables, index)
		if err != nil {
			return nil, nil, err
		}
		result, err := resultForNodeContext(ctx, callable.node, source, path, relative, callable.kind, owner, signature, identity, stableOwner, occurrence, match.spans, owned, changes, options.CRAPThreshold, language)
		if err != nil {
			return nil, nil, err
		}
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

func collectCallableNodes(ctx context.Context, node *treesitter.Node, language languageDefinition, depth int, results *[]callableNode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if kind, ok := language.callableKind(node); ok {
		*results = append(*results, callableNode{node: node, kind: kind, depth: depth})
		depth++
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		if err := collectCallableNodes(ctx, node.NamedChild(index), language, depth, results); err != nil {
			return err
		}
	}
	return nil
}

func resultForNodeContext(ctx context.Context, node *treesitter.Node, source []byte, sourcePath, file, kind, name, signature, identity, stableOwner string, occurrence int, coverage []coverageSpan, owned []lineRange, changes changedFiles, threshold float64, language languageDefinition) (MethodResult, error) {
	start := int(node.StartPosition().Row) + 1
	end := int(node.EndPosition().Row) + 1
	complexity, err := complexityContext(ctx, node, source, language)
	if err != nil {
		return MethodResult{}, err
	}
	covered, err := methodCoverageContext(ctx, coverage, owned)
	if err != nil {
		return MethodResult{}, err
	}
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
	}, nil
}

func lexicalSignature(node *treesitter.Node, source []byte, language languageDefinition) string {
	end := node.EndByte()
	if body := language.callableBody(node); body != nil {
		end = body.StartByte()
	}
	return strings.Join(strings.Fields(string(source[node.StartByte():end])), " ")
}

func callableIdentity(node *treesitter.Node, source []byte, language languageDefinition) string {
	identity, _ := callableIdentityContext(context.Background(), node, source, language)
	return identity
}

func callableIdentityContext(ctx context.Context, node *treesitter.Node, source []byte, language languageDefinition) (string, error) {
	return callableStructuralIdentityContext(ctx, node, source, language.callableBody(node))
}

func callableStructuralIdentity(node *treesitter.Node, source []byte, body *treesitter.Node) string {
	identity, _ := callableStructuralIdentityContext(context.Background(), node, source, body)
	return identity
}

func callableStructuralIdentityContext(ctx context.Context, node *treesitter.Node, source []byte, body *treesitter.Node) (string, error) {
	var identity strings.Builder
	var visit func(*treesitter.Node) error
	visit = func(current *treesitter.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if body != nil && current.StartByte() == body.StartByte() && current.EndByte() == body.EndByte() {
			return nil
		}
		if strings.Contains(current.Kind(), "comment") {
			return nil
		}
		if current.ChildCount() == 0 {
			identity.WriteString(current.Kind())
			identity.WriteByte(':')
			identity.Write(source[current.StartByte():current.EndByte()])
			identity.WriteByte(0)
			return nil
		}
		for index := uint(0); index < current.ChildCount(); index++ {
			if err := visit(current.Child(index)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(node); err != nil {
		return "", err
	}
	return identity.String(), nil
}

func callableOwnerIdentity(node *treesitter.Node, source []byte, language languageDefinition) string {
	identity, _ := callableOwnerIdentityContext(context.Background(), node, source, language)
	return identity
}

func callableOwnerIdentityContext(ctx context.Context, node *treesitter.Node, source []byte, language languageDefinition) (string, error) {
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
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, ok := language.callableKind(parent); ok {
			name := language.qualifiedName(parent, source)
			if strings.Contains(name, "<anonymous@") {
				name = "anonymous"
			}
			identity, err := callableIdentityContext(ctx, parent, source, language)
			if err != nil {
				return "", err
			}
			add("callable:" + name + ":" + identity)
		}
		if language.ownerName != nil {
			add(language.ownerName(parent, source))
		}
	}
	if len(parts) == 0 {
		return "anonymous", nil
	}
	return strings.Join(parts, "\x00"), nil
}

func nodeSource(node *treesitter.Node, source []byte) string {
	if node == nil {
		return ""
	}
	return string(source[node.StartByte():node.EndByte()])
}

func callableOwnedRanges(callables []callableNode, target int) []lineRange {
	ranges, _ := callableOwnedRangesContext(context.Background(), callables, target)
	return ranges
}

func callableOwnedRangesContext(ctx context.Context, callables []callableNode, target int) ([]lineRange, error) {
	node := callables[target].node
	owned := []lineRange{{Start: int(node.StartPosition().Row) + 1, End: int(node.EndPosition().Row) + 1}}
	excluded := make([]lineRange, 0)
	for index, candidate := range callables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if index == target || !callablePrecedes(candidate, callables[target]) {
			continue
		}
		excluded = append(excluded, lineRange{Start: int(candidate.node.StartPosition().Row) + 1, End: int(candidate.node.EndPosition().Row) + 1})
	}
	for _, excluded := range mergeRanges(excluded) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	return owned, nil
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
	value, _ := complexityContext(context.Background(), root, source, language)
	return value
}

func complexityContext(ctx context.Context, root *treesitter.Node, source []byte, language languageDefinition) (int, error) {
	value := 1
	var visit func(*treesitter.Node, bool) error
	visit = func(node *treesitter.Node, isRoot bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !isRoot {
			if _, nestedCallable := language.callableKind(node); nestedCallable {
				return nil
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
			if err := visit(node.NamedChild(index), false); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root, true); err != nil {
		return 0, err
	}
	return value, nil
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
