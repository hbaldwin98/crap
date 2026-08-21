package analysis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
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
	return &Analyzer{languages: map[string]languageDefinition{
		".cs": {
			name: "csharp", parser: csharpParser, callableKinds: csharpCallableKinds, branchKinds: csharpBranchKinds,
			logicalOps: map[string]bool{"&&": true, "||": true, "??": true}, qualifiedName: csharpQualifiedName,
		},
		".go": {
			name: "go", parser: goParser, callableKinds: goCallableKinds, branchKinds: goBranchKinds,
			logicalOps: map[string]bool{"&&": true, "||": true}, qualifiedName: goQualifiedName,
		},
	}}, nil
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
	files, err := findSourceFiles(root, options.Paths, options.IncludeTests)
	if err != nil {
		return Report{}, err
	}
	coveragePath := options.CoveragePath
	if coveragePath != "" && !filepath.IsAbs(coveragePath) {
		coveragePath = filepath.Join(root, coveragePath)
	}
	coverage, err := loadCoverage(coveragePath, root)
	if err != nil {
		return Report{}, err
	}
	changes, err := gitChangedLines(root, options.DiffBase)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		SchemaVersion: SchemaVersion,
		Mode:          "all",
		Coverage:      options.CoveragePath,
		DiffBase:      options.DiffBase,
		Threshold:     options.CRAPThreshold,
		Methods:       make([]MethodResult, 0),
	}
	if options.DiffBase != "" {
		report.Mode = "changed"
	}

	for _, file := range files {
		methods, err := analyzer.analyzeFile(root, file, coverage, changes, options)
		if err != nil {
			return Report{}, err
		}
		report.Methods = append(report.Methods, methods...)
	}
	sort.Slice(report.Methods, func(i, j int) bool {
		if report.Methods[i].File == report.Methods[j].File {
			return report.Methods[i].StartLine < report.Methods[j].StartLine
		}
		return report.Methods[i].File < report.Methods[j].File
	})
	report.Summary = summarize(report.Methods)
	report.Summary.Files = len(files)
	return report, nil
}

func (analyzer *Analyzer) analyzeFile(root, path string, coverage coverageData, changes changedLines, options Options) ([]MethodResult, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	language, ok := analyzer.languages[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, fmt.Errorf("unsupported source file %s", path)
	}
	tree := language.parser.Parse(source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse %s: parser returned no tree", path)
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return nil, fmt.Errorf("parse %s: source contains syntax errors", path)
	}

	relative, err := filepath.Rel(root, path)
	if err != nil {
		return nil, fmt.Errorf("make path relative: %w", err)
	}
	relative = normalizePath(relative)
	fileCoverage := coverage.forFile(relative)
	results := make([]MethodResult, 0)
	collectCallables(tree.RootNode(), source, relative, fileCoverage, changes, options, language, &results)
	return results, nil
}

func collectCallables(node *treesitter.Node, source []byte, file string, coverage []coverageSpan, changes changedLines, options Options, language languageDefinition, results *[]MethodResult) {
	if kind, ok := language.callableKinds[node.Kind()]; ok {
		result := resultForNode(node, source, file, kind, coverage, changes, options.CRAPThreshold, language)
		if options.DiffBase == "" || result.Changed {
			*results = append(*results, result)
		}
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		collectCallables(node.NamedChild(index), source, file, coverage, changes, options, language, results)
	}
}

func resultForNode(node *treesitter.Node, source []byte, file, kind string, coverage []coverageSpan, changes changedLines, threshold float64, language languageDefinition) MethodResult {
	start := int(node.StartPosition().Row) + 1
	end := int(node.EndPosition().Row) + 1
	name := language.qualifiedName(node, source)
	complexity := complexity(node, source, language)
	covered := methodCoverage(coverage, start, end)
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
		Changed:         changes.intersects(file, start, end),
		AboveThreshold:  score > threshold,
	}
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

type sourceCollector struct {
	includeTests bool
	seen         map[string]bool
	files        []string
}

func findSourceFiles(root string, paths []string, includeTests bool) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	collector := sourceCollector{includeTests: includeTests, seen: make(map[string]bool)}
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
	collector.collect(path)
	return nil
}

func (collector *sourceCollector) visit(path string, entry fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() && ignoredDirectory(entry.Name()) {
		return filepath.SkipDir
	}
	if !entry.IsDir() {
		collector.collect(path)
	}
	return nil
}

func (collector *sourceCollector) collect(path string) {
	if isSourceFile(path, collector.includeTests) && !collector.seen[path] {
		collector.seen[path] = true
		collector.files = append(collector.files, path)
	}
}

func ignoredDirectory(name string) bool {
	return name == ".git" || name == "bin" || name == "obj" || name == "node_modules"
}

func isSourceFile(path string, includeTests bool) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".go" && !includeTests && strings.HasSuffix(strings.ToLower(path), "_test.go") {
		return false
	}
	return extension == ".cs" || extension == ".go"
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
