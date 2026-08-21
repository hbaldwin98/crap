package analysis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hbaldwin98/crap/internal/rootauth"
	treesitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var csharpCallableKinds = map[string]string{
	"method_declaration":              "method",
	"constructor_declaration":         "constructor",
	"destructor_declaration":          "destructor",
	"operator_declaration":            "operator",
	"conversion_operator_declaration": "conversion_operator",
	"local_function_statement":        "local_function",
	"accessor_declaration":            "accessor",
}

var csharpBranchKinds = map[string]bool{
	"if_statement":              true,
	"for_statement":             true,
	"foreach_statement":         true,
	"while_statement":           true,
	"do_statement":              true,
	"catch_clause":              true,
	"conditional_expression":    true,
	"case_switch_label":         true,
	"case_pattern_switch_label": true,
	"switch_expression_arm":     true,
	"and_pattern":               true,
	"or_pattern":                true,
}

var goCallableKinds = map[string]string{
	"function_declaration": "function",
	"method_declaration":   "method",
}

var goBranchKinds = map[string]bool{
	"if_statement":  true,
	"for_statement": true,
}

var typescriptCallableKinds = map[string]string{
	"function_declaration":           "function",
	"generator_function_declaration": "generator_function",
	"function_expression":            "function",
	"generator_function":             "generator_function",
	"arrow_function":                 "arrow_function",
	"method_definition":              "method",
}

var typescriptBranchKinds = map[string]bool{
	"if_statement":       true,
	"for_statement":      true,
	"for_in_statement":   true,
	"while_statement":    true,
	"do_statement":       true,
	"catch_clause":       true,
	"switch_case":        true,
	"ternary_expression": true,
}

type languageDefinition struct {
	name          string
	parser        *treesitter.Parser
	callableKinds map[string]string
	branchKinds   map[string]bool
	logicalOps    map[string]bool
	qualifiedName func(*treesitter.Node, []byte) string
}

type Analyzer struct {
	languages map[string]languageDefinition
	git       gitRunner
}

func NewAnalyzer() (*Analyzer, error) {
	csharpParser := treesitter.NewParser()
	if err := csharpParser.SetLanguage(treesitter.NewLanguage(tree_sitter_c_sharp.Language())); err != nil {
		csharpParser.Close()
		return nil, fmt.Errorf("load C# grammar: %w", err)
	}
	goParser := treesitter.NewParser()
	if err := goParser.SetLanguage(treesitter.NewLanguage(tree_sitter_go.Language())); err != nil {
		csharpParser.Close()
		goParser.Close()
		return nil, fmt.Errorf("load Go grammar: %w", err)
	}
	typescriptParser := treesitter.NewParser()
	if err := typescriptParser.SetLanguage(treesitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())); err != nil {
		csharpParser.Close()
		goParser.Close()
		typescriptParser.Close()
		return nil, fmt.Errorf("load TypeScript grammar: %w", err)
	}
	tsxParser := treesitter.NewParser()
	if err := tsxParser.SetLanguage(treesitter.NewLanguage(tree_sitter_typescript.LanguageTSX())); err != nil {
		csharpParser.Close()
		goParser.Close()
		typescriptParser.Close()
		tsxParser.Close()
		return nil, fmt.Errorf("load TSX grammar: %w", err)
	}
	return &Analyzer{languages: map[string]languageDefinition{
		".cs": {
			name: "csharp", parser: csharpParser, callableKinds: csharpCallableKinds, branchKinds: csharpBranchKinds,
			logicalOps: map[string]bool{"&&": true, "||": true, "??": true}, qualifiedName: csharpQualifiedName,
		},
		".go": {
			name: "go", parser: goParser, callableKinds: goCallableKinds, branchKinds: goBranchKinds,
			logicalOps: map[string]bool{"&&": true, "||": true}, qualifiedName: goQualifiedName,
		},
		".ts": {
			name: "typescript", parser: typescriptParser, callableKinds: typescriptCallableKinds, branchKinds: typescriptBranchKinds,
			logicalOps: map[string]bool{"&&": true, "||": true, "??": true}, qualifiedName: typescriptQualifiedName,
		},
		".tsx": {
			name: "typescript", parser: tsxParser, callableKinds: typescriptCallableKinds, branchKinds: typescriptBranchKinds,
			logicalOps: map[string]bool{"&&": true, "||": true, "??": true}, qualifiedName: typescriptQualifiedName,
		},
	}, git: execGitRunner{}}, nil
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
		Mode:           "all",
		Coverage:       options.CoveragePath,
		DiffBase:       options.DiffBase,
		DiffBaseCommit: changes.BaseCommit,
		DiffHeadCommit: changes.HeadCommit,
		DiffMergeBase:  changes.MergeBase,
		Threshold:      options.CRAPThreshold,
		Methods:        make([]MethodResult, 0),
	}
	if options.DiffBase != "" {
		report.Mode = "changed"
	}
	relativeFiles := make([]string, len(files))
	for index, file := range files {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return Report{}, fmt.Errorf("make path relative: %w", err)
		}
		relativeFiles[index] = normalizePath(relative)
	}
	coverageMatches := coverage.matchFiles(relativeFiles)

	for index, file := range files {
		methods, diagnostic, err := analyzer.analyzeFile(file, relativeFiles[index], coverageMatches[index], changes, options)
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

func (analyzer *Analyzer) analyzeFile(path, relative string, match coverageMatch, changes changedFiles, options Options) ([]MethodResult, *Diagnostic, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
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
	for index, callable := range callables {
		result := resultForNode(callable.node, source, path, relative, callable.kind, match.spans, callableOwnedRanges(callables, index), changes, options.CRAPThreshold, language)
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
	if kind, ok := language.callableKinds[node.Kind()]; ok {
		*results = append(*results, callableNode{node: node, kind: kind, depth: depth})
		depth++
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		collectCallableNodes(node.NamedChild(index), language, depth, results)
	}
}

func resultForNode(node *treesitter.Node, source []byte, sourcePath, file, kind string, coverage []coverageSpan, owned []lineRange, changes changedFiles, threshold float64, language languageDefinition) MethodResult {
	start := int(node.StartPosition().Row) + 1
	end := int(node.EndPosition().Row) + 1
	name := language.qualifiedName(node, source)
	complexity := complexity(node, source, language)
	covered := methodCoverage(coverage, owned)
	coverageForScore := 0.0
	if covered != nil {
		coverageForScore = *covered
	}
	score := CRAPScore(complexity, coverageForScore)
	return MethodResult{
		ID:              fmt.Sprintf("%s:%d:%s", file, start, name),
		Language:        language.name,
		File:            file,
		Name:            name,
		Kind:            kind,
		StartLine:       start,
		EndLine:         end,
		Complexity:      complexity,
		CoveragePercent: covered,
		CRAP:            score,
		Changed:         changes.intersects(sourcePath, start, end),
		AboveThreshold:  score > threshold,
	}
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
			if _, nestedCallable := language.callableKinds[node.Kind()]; nestedCallable {
				return
			}
		}
		if language.branchKinds[node.Kind()] || isNonDefaultGoCase(node, source, language.name) {
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

func isNonDefaultGoCase(node *treesitter.Node, source []byte, language string) bool {
	if language != "go" || (node.Kind() != "expression_case" && node.Kind() != "type_case" && node.Kind() != "communication_case") {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(node.Utf8Text(source)), "default")
}

func csharpQualifiedName(node *treesitter.Node, source []byte) string {
	parts := make([]string, 0)
	root := node
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		root = parent
		switch parent.Kind() {
		case "namespace_declaration", "file_scoped_namespace_declaration", "class_declaration", "struct_declaration", "record_declaration", "interface_declaration":
			if name := parent.ChildByFieldName("name"); name != nil {
				parts = append(parts, name.Utf8Text(source))
			}
		}
	}
	// The grammar represents a file-scoped namespace as a sibling of its types.
	for index := uint(0); index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child.Kind() == "file_scoped_namespace_declaration" {
			if name := child.ChildByFieldName("name"); name != nil {
				parts = append(parts, name.Utf8Text(source))
			}
			break
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}

	name := node.ChildByFieldName("name")
	methodName := node.Kind()
	if name != nil {
		methodName = name.Utf8Text(source)
	}
	if node.Kind() == "accessor_declaration" {
		owner := node.Parent()
		if owner != nil {
			owner = owner.Parent()
		}
		if owner != nil {
			if ownerName := owner.ChildByFieldName("name"); ownerName != nil {
				methodName = ownerName.Utf8Text(source) + "." + methodName
			}
		}
	}
	parts = append(parts, methodName)
	return strings.Join(parts, ".")
}

func goQualifiedName(node *treesitter.Node, source []byte) string {
	root := node
	for root.Parent() != nil {
		root = root.Parent()
	}
	parts := make([]string, 0, 3)
	for index := uint(0); index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child.Kind() == "package_clause" && child.NamedChildCount() > 0 {
			parts = append(parts, child.NamedChild(0).Utf8Text(source))
			break
		}
	}
	if receiver := node.ChildByFieldName("receiver"); receiver != nil {
		text := strings.TrimSpace(receiver.Utf8Text(source))
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		fields := strings.Fields(text)
		if len(fields) > 0 {
			parts = append(parts, "("+fields[len(fields)-1]+")")
		}
	}
	name := node.ChildByFieldName("name")
	if name != nil {
		parts = append(parts, name.Utf8Text(source))
	} else {
		parts = append(parts, node.Kind())
	}
	return strings.Join(parts, ".")
}

func typescriptQualifiedName(node *treesitter.Node, source []byte) string {
	name := node.ChildByFieldName("name")
	if name == nil {
		name = typescriptAssignedName(node)
	}
	callableName := "<anonymous>"
	if name != nil {
		callableName = strings.TrimSpace(name.Utf8Text(source))
	} else {
		position := node.StartPosition()
		callableName = fmt.Sprintf("<anonymous@%d:%d>", position.Row+1, position.Column+1)
	}

	parts := make([]string, 0, 3)
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "class_declaration", "abstract_class_declaration", "internal_module":
			if parentName := parent.ChildByFieldName("name"); parentName != nil {
				parts = append(parts, parentName.Utf8Text(source))
			}
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	parts = append(parts, callableName)
	return strings.Join(parts, ".")
}

func typescriptAssignedName(node *treesitter.Node) *treesitter.Node {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "variable_declarator", "public_field_definition":
			return parent.ChildByFieldName("name")
		case "pair":
			return parent.ChildByFieldName("key")
		case "assignment_expression":
			return parent.ChildByFieldName("left")
		case "parenthesized_expression", "as_expression", "satisfies_expression", "type_assertion", "non_null_expression":
			continue
		default:
			return nil
		}
	}
	return nil
}

type sourceCollector struct {
	includeTests  bool
	seen          map[string]bool
	files         []string
	authorization *rootauth.Root
}

func findSourceFiles(root string, paths []string, includeTests bool, authorization *rootauth.Root) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	collector := sourceCollector{includeTests: includeTests, seen: make(map[string]bool), authorization: authorization}
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
	}
	if isSourceFile(path, collector.includeTests) && !collector.seen[path] {
		collector.seen[path] = true
		collector.files = append(collector.files, path)
	}
	return nil
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
