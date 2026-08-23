package analysis

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/hbaldwin98/crap/internal/reportcontract"
	"github.com/hbaldwin98/crap/internal/rootauth"
)

const (
	CallGraphSchemaVersion             = "1"
	MaximumCallGraphEdges              = 250_000
	MaximumCallGraphCallSitesPerEdge   = 25
	MaximumCallGraphUnresolvedCalls    = 5_000
	MaximumCallGraphAffectedTestSeeds  = 10_000
	maximumCallGraphErrorDiagnostics   = 5
	callGraphTestNamePrefix            = "Test"
	callGraphTestFileSuffix            = "_test.go"
	callGraphPackagePattern            = "./..."
	callGraphPackageLoadingEnvironment = "GOWORK=off"
)

type CallGraphOptions struct {
	Paths            []string
	Root             string
	DiffBase         string
	IncludeGenerated bool
	Exclude          []string
	Authorization    *rootauth.Root
}

type CallGraphReport struct {
	SchemaVersion   string                      `json:"schemaVersion"`
	ReportType      string                      `json:"reportType"`
	Tool            reportcontract.ToolIdentity `json:"tool"`
	Compiler        CallGraphCompiler           `json:"compiler"`
	Module          CallGraphModule             `json:"module"`
	Fingerprints    reportcontract.Fingerprints `json:"fingerprints"`
	Coordinates     reportcontract.Coordinates  `json:"coordinates"`
	Grammars        []GrammarIdentity           `json:"grammars"`
	DiffBase        string                      `json:"diffBase,omitempty"`
	BaseCommit      string                      `json:"baseCommit,omitempty"`
	HeadCommit      string                      `json:"headCommit,omitempty"`
	MergeBase       string                      `json:"mergeBase,omitempty"`
	Policy          CallGraphPolicy             `json:"policy"`
	Summary         CallGraphSummary            `json:"summary"`
	Functions       []CallGraphFunction         `json:"functions"`
	Edges           []CallGraphEdge             `json:"edges"`
	UnresolvedCalls []CallGraphUnresolvedCall   `json:"unresolvedCalls"`
	AffectedTests   []CallGraphAffectedTest     `json:"affectedTests"`
	Limitations     []string                    `json:"limitations"`
	Diagnostics     []Diagnostic                `json:"diagnostics"`
}

type CallGraphCompiler struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type CallGraphModule struct {
	Path      string `json:"path"`
	GoVersion string `json:"goVersion,omitempty"`
	SHA256    string `json:"sha256"`
}

type CallGraphPolicy struct {
	Resolution string `json:"resolution"`
	Dispatch   string `json:"dispatch"`
	Tests      string `json:"tests"`
}

type CallGraphSummary struct {
	Packages            int  `json:"packages"`
	Functions           int  `json:"functions"`
	UnmatchedCallables  int  `json:"unmatchedCallables"`
	Tests               int  `json:"tests"`
	Edges               int  `json:"edges"`
	StaticEdges         int  `json:"staticEdges"`
	DispatchEdges       int  `json:"dispatchEdges"`
	CallSites           int  `json:"callSites"`
	UnresolvedCalls     int  `json:"unresolvedCalls"`
	ChangedCallables    int  `json:"changedCallables"`
	AffectedTests       int  `json:"affectedTests"`
	TruncatedCallSites  bool `json:"truncatedCallSites"`
	TruncatedUnresolved bool `json:"truncatedUnresolved"`
}

type CallGraphFunction struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	File         string `json:"file"`
	Package      string `json:"package"`
	CompilerName string `json:"compilerName,omitempty"`
	Test         bool   `json:"test"`
	Changed      bool   `json:"changed"`
	StartLine    int    `json:"startLine"`
	StartColumn  int    `json:"startColumn"`
	EndLine      int    `json:"endLine"`
	EndColumn    int    `json:"endColumn"`
}

type CallGraphCallSite struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type CallGraphEdge struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"`
	From        string              `json:"from"`
	To          string              `json:"to"`
	Occurrences int                 `json:"occurrences"`
	CallSites   []CallGraphCallSite `json:"callSites"`
}

type CallGraphUnresolvedCall struct {
	Caller string `json:"caller,omitempty"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Reason string `json:"reason"`
}

type CallGraphAffectedTest struct {
	Test     string   `json:"test"`
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Distance int      `json:"distance"`
	Seeds    []string `json:"seeds"`
}

type callGraphSiteKey struct {
	path   string
	line   int
	column int
}

type callGraphEdgeKey struct {
	from string
	to   string
	kind string
}

type callGraphEdgeBuild struct {
	edge      CallGraphEdge
	sites     map[callGraphSiteKey]struct{}
	truncated bool
}

type callGraphWalker struct {
	fset        *token.FileSet
	moduleRoot  string
	inventory   map[callGraphSiteKey]MethodResult
	names       map[callGraphSiteKey]MethodResult
	functions   map[callGraphSiteKey]CallGraphFunction
	edges       map[callGraphEdgeKey]*callGraphEdgeBuild
	unresolved  map[CallGraphUnresolvedCall]struct{}
	implementer *callGraphImplementer
	packages    []*packages.Package
}

func (analyzer *Analyzer) AnalyzeCallGraph(options CallGraphOptions) (CallGraphReport, error) {
	return analyzer.AnalyzeCallGraphContext(context.Background(), options)
}

func (analyzer *Analyzer) AnalyzeCallGraphContext(ctx context.Context, options CallGraphOptions) (CallGraphReport, error) {
	moduleRoot, err := callGraphModuleRoot(options.Root, options.Paths)
	if err != nil {
		return CallGraphReport{}, err
	}
	report, _, err := analyzer.analyzeContext(ctx, Options{
		Paths: []string{"."}, Root: moduleRoot, DiffBase: options.DiffBase, CRAPThreshold: 30,
		IncludeTests: true, IncludeGenerated: options.IncludeGenerated, Exclude: options.Exclude,
		Authorization: options.Authorization,
	})
	if err != nil {
		return CallGraphReport{}, err
	}
	graph, err := buildCallGraph(ctx, moduleRoot, report)
	if err != nil {
		return CallGraphReport{}, err
	}
	return graph, nil
}

func buildCallGraph(ctx context.Context, moduleRoot string, report Report) (CallGraphReport, error) {
	modulePath, moduleFingerprint, err := loadCodeGraphGoModule(moduleRoot)
	if err != nil {
		return CallGraphReport{}, err
	}
	if moduleFingerprint == nil {
		return CallGraphReport{}, fmt.Errorf("call graph requires a go.mod at %s", moduleRoot)
	}
	loaded, err := loadCallGraphPackages(ctx, moduleRoot)
	if err != nil {
		return CallGraphReport{}, err
	}
	graph := newCallGraphReport(report, modulePath, moduleFingerprint, loaded)
	walker := newCallGraphWalker(loaded, moduleRoot, report)
	if err := walker.walk(); err != nil {
		return CallGraphReport{}, err
	}
	graph.Functions = walker.sortedFunctions()
	graph.Edges, graph.UnresolvedCalls, graph.Summary.TruncatedCallSites, graph.Summary.TruncatedUnresolved = walker.sortedEdges()
	graph.Summary.StaticEdges, graph.Summary.DispatchEdges = countCallGraphEdgeKinds(graph.Edges)
	graph.Summary.CallSites = countCallGraphCallSites(graph.Edges)
	graph.Summary.Edges = len(graph.Edges)
	if report.DiffBase != "" {
		graph.AffectedTests = buildAffectedTests(graph.Edges, graph.Functions)
		graph.Summary.AffectedTests = len(graph.AffectedTests)
	}
	finalizeCallGraphSummary(&graph.Summary, len(walker.packages), graph.Functions, graph.UnresolvedCalls, report)
	if len(graph.Edges) > MaximumCallGraphEdges {
		return CallGraphReport{}, fmt.Errorf("call graph has %d edges; maximum %d", len(graph.Edges), MaximumCallGraphEdges)
	}
	return graph, nil
}

func newCallGraphReport(report Report, modulePath string, moduleFingerprint *reportcontract.FileFingerprint, loaded loadedCallGraphPackages) CallGraphReport {
	fingerprints := report.Fingerprints
	fingerprints.Sources = goFileFingerprints(report.Fingerprints.Sources)
	fingerprints.ConfigSHA256 = reportcontract.JSONFingerprint(struct {
		Contract       string `json:"contract"`
		AnalysisConfig string `json:"analysisConfig"`
		ModuleSHA256   string `json:"moduleSha256"`
		DiffBase       string `json:"diffBase"`
		Dispatch       string `json:"dispatch"`
		Tests          string `json:"tests"`
	}{CallGraphSchemaVersion, report.Fingerprints.ConfigSHA256, moduleFingerprint.SHA256, report.DiffBase, "bounded-in-module-implementations-v1", "test-file-and-name-prefix-v1"})
	goVersion := moduleGoVersion(loaded.packages)
	return CallGraphReport{
		SchemaVersion: CallGraphSchemaVersion, ReportType: "call-graph", Tool: report.Tool,
		Compiler:     CallGraphCompiler{Name: "go", Version: loaded.goVersion},
		Module:       CallGraphModule{Path: modulePath, GoVersion: goVersion, SHA256: moduleFingerprint.SHA256},
		Fingerprints: fingerprints, Coordinates: report.Coordinates, Grammars: append([]GrammarIdentity(nil), report.Grammars...),
		DiffBase: report.DiffBase, BaseCommit: report.DiffBaseCommit, HeadCommit: report.DiffHeadCommit, MergeBase: report.DiffMergeBase,
		Policy:    CallGraphPolicy{Resolution: "compiler-types-info-v1", Dispatch: "bounded-in-module-implementations-v1", Tests: "test-file-and-name-prefix-v1"},
		Functions: make([]CallGraphFunction, 0), Edges: make([]CallGraphEdge, 0), UnresolvedCalls: make([]CallGraphUnresolvedCall, 0),
		AffectedTests: make([]CallGraphAffectedTest, 0),
		Limitations: []string{
			"call edges are resolved from compiler type information; runtime behavior and reflection are not modeled",
			"interface and type-parameter dispatch expands only to implementations declared in the analyzed Go module",
			"function values, method values, cgo calls, and generic instantiations of generic implementations are not expanded",
			"callables excluded by build constraints or unmatched to compiler positions are not connected",
			"deleted callables and deleted tests are not modeled",
			"affected tests are bounded by compiler facts and do not prove full behavioral coverage",
			"package loading disables Go workspaces and analyzes the module in module mode",
		},
		Diagnostics: append(make([]Diagnostic, 0, len(report.Diagnostics)), report.Diagnostics...),
	}
}

func goFileFingerprints(sources []reportcontract.FileFingerprint) []reportcontract.FileFingerprint {
	filtered := make([]reportcontract.FileFingerprint, 0, len(sources))
	for _, source := range sources {
		if strings.HasSuffix(source.Path, ".go") {
			filtered = append(filtered, source)
		}
	}
	return filtered
}

func moduleGoVersion(pkgs []*packages.Package) string {
	version := ""
	for _, pkg := range pkgs {
		if pkg.Module != nil && pkg.Module.GoVersion > version {
			version = pkg.Module.GoVersion
		}
	}
	return version
}

type loadedCallGraphPackages struct {
	packages  []*packages.Package
	fset      *token.FileSet
	goVersion string
}

func loadCallGraphPackages(ctx context.Context, moduleRoot string) (loadedCallGraphPackages, error) {
	fset := token.NewFileSet()
	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports | packages.NeedModule,
		Dir:     moduleRoot,
		Env:     append(os.Environ(), callGraphPackageLoadingEnvironment),
		Tests:   true,
		Fset:    fset,
	}
	loaded, err := packages.Load(cfg, callGraphPackagePattern)
	if err != nil {
		return loadedCallGraphPackages{}, fmt.Errorf("load Go packages: %w", err)
	}
	if err := callGraphPackageErrors(loaded); err != nil {
		return loadedCallGraphPackages{}, err
	}
	version, err := callGraphGoVersion(moduleRoot)
	if err != nil {
		return loadedCallGraphPackages{}, err
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].PkgPath < loaded[j].PkgPath })
	return loadedCallGraphPackages{packages: loaded, fset: fset, goVersion: version}, nil
}

func callGraphPackageErrors(loaded []*packages.Package) error {
	messages := make([]string, 0)
	for _, pkg := range loaded {
		for _, pkgErr := range pkg.Errors {
			messages = append(messages, fmt.Sprintf("%s: %s", pkg.PkgPath, pkgErr.Error()))
			if len(messages) >= maximumCallGraphErrorDiagnostics {
				break
			}
		}
		if len(messages) >= maximumCallGraphErrorDiagnostics {
			break
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return fmt.Errorf("load Go packages: %s; run go build ./... for details", strings.Join(messages, "; "))
}

func callGraphGoVersion(moduleRoot string) (string, error) {
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Dir = moduleRoot
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read Go toolchain version: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func callGraphModuleRoot(root string, paths []string) (string, error) {
	candidates := paths
	if len(candidates) == 0 {
		candidates = []string{root}
	}
	modules := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		start := candidate
		if info, err := os.Lstat(candidate); err == nil && !info.IsDir() {
			start = filepath.Dir(candidate)
		}
		module, err := nearestGoModuleRoot(start)
		if err != nil {
			return "", err
		}
		if module == "" {
			return "", fmt.Errorf("no go.mod found above %s", candidate)
		}
		modules[module] = struct{}{}
	}
	if len(modules) > 1 {
		return "", fmt.Errorf("call graph requires a single Go module; paths span %d modules", len(modules))
	}
	for module := range modules {
		return module, nil
	}
	return "", fmt.Errorf("no go.mod found above %s", root)
}

func nearestGoModuleRoot(start string) (string, error) {
	directory := start
	if !filepath.IsAbs(directory) {
		absolute, err := filepath.Abs(directory)
		if err != nil {
			return "", fmt.Errorf("resolve path %s: %w", start, err)
		}
		directory = absolute
	}
	for {
		info, err := os.Lstat(filepath.Join(directory, "go.mod"))
		if err == nil && info.Mode().IsRegular() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", nil
		}
		directory = parent
	}
}

func newCallGraphWalker(loaded loadedCallGraphPackages, moduleRoot string, report Report) *callGraphWalker {
	inventory := make(map[callGraphSiteKey]MethodResult, len(report.Methods))
	for _, method := range report.Methods {
		if method.Language != "go" {
			continue
		}
		key := callGraphSiteKey{path: method.File, line: method.StartLine, column: method.StartColumn}
		if _, exists := inventory[key]; !exists {
			inventory[key] = method
		}
	}
	return &callGraphWalker{
		fset: loaded.fset, moduleRoot: moduleRoot, inventory: inventory,
		names:       make(map[callGraphSiteKey]MethodResult),
		functions:   make(map[callGraphSiteKey]CallGraphFunction),
		edges:       make(map[callGraphEdgeKey]*callGraphEdgeBuild),
		unresolved:  make(map[CallGraphUnresolvedCall]struct{}),
		implementer: newCallGraphImplementer(loaded.packages, moduleRoot),
		packages:    inModulePackages(loaded.packages, moduleRoot),
	}
}

func inModulePackages(pkgs []*packages.Package, moduleRoot string) []*packages.Package {
	inModule := make([]*packages.Package, 0, len(pkgs))
	for _, pkg := range pkgs {
		if packageHasInModuleFile(pkg, moduleRoot) {
			inModule = append(inModule, pkg)
		}
	}
	sort.Slice(inModule, func(i, j int) bool {
		if inModule[i].PkgPath != inModule[j].PkgPath {
			return inModule[i].PkgPath < inModule[j].PkgPath
		}
		return inModule[i].ID < inModule[j].ID
	})
	return inModule
}

func packageHasInModuleFile(pkg *packages.Package, moduleRoot string) bool {
	for _, file := range pkg.GoFiles {
		relative, err := filepath.Rel(moduleRoot, file)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".." {
			return true
		}
	}
	return false
}

func (walker *callGraphWalker) walk() error {
	for _, pkg := range walker.packages {
		walker.declarePackage(pkg)
	}
	for _, pkg := range walker.packages {
		for _, file := range pkg.Syntax {
			walker.walkPackageFile(pkg, file, nil)
		}
	}
	return nil
}

func (walker *callGraphWalker) declarePackage(pkg *packages.Package) {
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			switch function := node.(type) {
			case *ast.FuncDecl:
				walker.declareFunction(pkg, function)
			case *ast.FuncLit:
				walker.declareFunction(pkg, function)
			}
			return true
		})
	}
}

func (walker *callGraphWalker) walkPackageFile(pkg *packages.Package, file *ast.File, caller *callGraphSiteKey) {
	ast.Inspect(file, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			walker.walkBody(pkg, function.Body, walker.functionKey(function.Pos()))
			return false
		case *ast.FuncLit:
			walker.walkBody(pkg, function.Body, walker.functionKey(function.Pos()))
			return false
		case *ast.CallExpr:
			walker.recordCall(pkg, caller, function)
			return true
		}
		return true
	})
}

func (walker *callGraphWalker) functionKey(position token.Pos) *callGraphSiteKey {
	key := walker.siteKey(position)
	return &key
}

func (walker *callGraphWalker) walkBody(pkg *packages.Package, body *ast.BlockStmt, caller *callGraphSiteKey) {
	if body == nil {
		return
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			walker.walkBody(pkg, function.Body, walker.functionKey(function.Pos()))
			return false
		case *ast.FuncLit:
			walker.walkBody(pkg, function.Body, walker.functionKey(function.Pos()))
			return false
		case *ast.CallExpr:
			walker.recordCall(pkg, caller, function)
			return true
		}
		return true
	})
}

func (walker *callGraphWalker) siteKey(position token.Pos) callGraphSiteKey {
	resolved := walker.fset.Position(position)
	return callGraphSiteKey{path: walker.relativePath(resolved.Filename), line: resolved.Line, column: resolved.Column}
}

func (walker *callGraphWalker) relativePath(absolute string) string {
	relative, err := filepath.Rel(walker.moduleRoot, absolute)
	if err != nil {
		return normalizePath(absolute)
	}
	return normalizePath(relative)
}

func (walker *callGraphWalker) declareFunction(pkg *packages.Package, function ast.Node) {
	key := walker.siteKey(function.Pos())
	if _, exists := walker.functions[key]; exists {
		return
	}
	declared, ok := walker.inventory[key]
	if !ok {
		return
	}
	entry := CallGraphFunction{
		ID: declared.ID, Name: declared.Name, Kind: declared.Kind, File: declared.File, Package: pkg.PkgPath,
		CompilerName: walker.compilerFunctionName(pkg, function),
		Test:         isCallGraphTestFunction(declared.File, declared.Name),
		Changed:      declared.Changed,
		StartLine:    declared.StartLine, StartColumn: declared.StartColumn, EndLine: declared.EndLine, EndColumn: declared.EndColumn,
	}
	walker.functions[key] = entry
	if decl, isDecl := function.(*ast.FuncDecl); isDecl {
		walker.names[walker.siteKey(decl.Name.Pos())] = declared
	}
}

func (walker *callGraphWalker) lookupDeclaration(position token.Pos) (MethodResult, bool) {
	key := walker.siteKey(position)
	if method, ok := walker.names[key]; ok {
		return method, true
	}
	method, ok := walker.inventory[key]
	return method, ok
}

func (walker *callGraphWalker) compilerFunctionName(pkg *packages.Package, function ast.Node) string {
	decl, ok := function.(*ast.FuncDecl)
	if !ok {
		return ""
	}
	if object, ok := pkg.TypesInfo.Defs[decl.Name].(*types.Func); ok {
		return object.FullName()
	}
	return ""
}

func isCallGraphTestFunction(file, name string) bool {
	return strings.HasSuffix(file, callGraphTestFileSuffix) && strings.HasPrefix(callGraphBareName(name), callGraphTestNamePrefix)
}

func callGraphBareName(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index+1:]
	}
	return name
}

func (walker *callGraphWalker) recordCall(pkg *packages.Package, caller *callGraphSiteKey, call *ast.CallExpr) {
	site := walker.siteKey(call.Fun.Pos())
	expression := callGraphCalleeExpression(call.Fun)
	callerID := walker.callerID(caller)
	switch node := expression.(type) {
	case *ast.Ident:
		walker.recordIdentCall(pkg, callerID, node, site)
	case *ast.SelectorExpr:
		walker.recordSelectorCall(pkg, callerID, node, site)
	case *ast.FuncLit:
		walker.recordStaticCall(callerID, walker.functionKey(node.Pos()), site)
	default:
		walker.recordUnresolvedCall(callerID, site, "function-value")
	}
}

func callGraphCalleeExpression(expression ast.Expr) ast.Expr {
	for {
		switch node := expression.(type) {
		case *ast.ParenExpr:
			expression = node.X
		case *ast.IndexExpr:
			expression = node.X
		case *ast.IndexListExpr:
			expression = node.X
		default:
			return expression
		}
	}
}

func (walker *callGraphWalker) callerID(caller *callGraphSiteKey) string {
	if caller == nil {
		return ""
	}
	if function, ok := walker.functions[*caller]; ok {
		return function.ID
	}
	return ""
}

func (walker *callGraphWalker) recordIdentCall(pkg *packages.Package, callerID string, identifier *ast.Ident, site callGraphSiteKey) {
	object := pkg.TypesInfo.Uses[identifier]
	switch target := object.(type) {
	case nil:
		walker.recordUnresolvedCall(callerID, site, "function-value")
	case *types.Builtin:
		walker.recordUnresolvedCall(callerID, site, "builtin")
	case *types.TypeName:
		walker.recordUnresolvedCall(callerID, site, "conversion")
	case *types.Func:
		walker.recordFuncCall(callerID, target, site)
	default:
		walker.recordUnresolvedCall(callerID, site, "function-value")
	}
}

func (walker *callGraphWalker) recordSelectorCall(pkg *packages.Package, callerID string, selector *ast.SelectorExpr, site callGraphSiteKey) {
	if selection, ok := pkg.TypesInfo.Selections[selector]; ok && selection.Kind() != types.FieldVal {
		target, isFunc := selection.Obj().(*types.Func)
		if !isFunc {
			walker.recordUnresolvedCall(callerID, site, "function-value")
			return
		}
		if selection.Kind() == types.MethodExpr {
			walker.recordFuncCall(callerID, target, site)
			return
		}
		if interfaceType, ok := selection.Recv().Underlying().(*types.Interface); ok {
			walker.recordDispatchCall(callerID, target, interfaceType, site)
			return
		}
		walker.recordFuncCall(callerID, target, site)
		return
	}
	walker.recordIdentCall(pkg, callerID, selector.Sel, site)
}

func (walker *callGraphWalker) recordFuncCall(callerID string, target *types.Func, site callGraphSiteKey) {
	function, ok := walker.lookupDeclaration(target.Pos())
	if !ok {
		if !walker.inModulePosition(target.Pos()) {
			walker.recordUnresolvedCall(callerID, site, "outside-module")
			return
		}
		walker.recordUnresolvedCall(callerID, site, "not-in-inventory")
		return
	}
	walker.addEdge(callerID, function.ID, "static", site)
}

func (walker *callGraphWalker) recordDispatchCall(callerID string, method *types.Func, interfaceType *types.Interface, site callGraphSiteKey) {
	implementations := walker.implementer.implementations(interfaceType, method)
	if len(implementations) == 0 {
		walker.recordUnresolvedCall(callerID, site, "no-in-module-implementations")
		return
	}
	for _, implementation := range implementations {
		function, ok := walker.lookupDeclaration(implementation.Pos())
		if !ok {
			continue
		}
		walker.addEdge(callerID, function.ID, "dispatch", site)
	}
}

func (walker *callGraphWalker) recordStaticCall(callerID string, callee *callGraphSiteKey, site callGraphSiteKey) {
	function, ok := walker.functions[*callee]
	if !ok {
		walker.recordUnresolvedCall(callerID, site, "not-in-inventory")
		return
	}
	walker.addEdge(callerID, function.ID, "static", site)
}

func (walker *callGraphWalker) inModulePosition(position token.Pos) bool {
	resolved := walker.fset.Position(position)
	if !filepath.IsAbs(resolved.Filename) {
		return true
	}
	relative, err := filepath.Rel(walker.moduleRoot, resolved.Filename)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".."
}

func (walker *callGraphWalker) addEdge(callerID, calleeID, kind string, site callGraphSiteKey) {
	if callerID == "" {
		walker.recordUnresolvedCall(callerID, site, "outside-function-body")
		return
	}
	key := callGraphEdgeKey{from: callerID, to: calleeID, kind: kind}
	build, ok := walker.edges[key]
	if !ok {
		build = &callGraphEdgeBuild{edge: CallGraphEdge{Kind: kind, From: callerID, To: calleeID}, sites: make(map[callGraphSiteKey]struct{})}
		walker.edges[key] = build
	}
	build.edge.Occurrences++
	if _, exists := build.sites[site]; !exists {
		if len(build.sites) < MaximumCallGraphCallSitesPerEdge {
			build.sites[site] = struct{}{}
		} else {
			build.truncated = true
		}
	}
}

func (walker *callGraphWalker) recordUnresolvedCall(callerID string, site callGraphSiteKey, reason string) {
	walker.unresolved[CallGraphUnresolvedCall{Caller: callerID, Path: site.path, Line: site.line, Column: site.column, Reason: reason}] = struct{}{}
}

func (walker *callGraphWalker) sortedFunctions() []CallGraphFunction {
	functions := make([]CallGraphFunction, 0, len(walker.functions))
	for _, function := range walker.functions {
		functions = append(functions, function)
	}
	sort.Slice(functions, func(i, j int) bool {
		if functions[i].File != functions[j].File {
			return functions[i].File < functions[j].File
		}
		if functions[i].StartLine != functions[j].StartLine {
			return functions[i].StartLine < functions[j].StartLine
		}
		return functions[i].StartColumn < functions[j].StartColumn
	})
	return functions
}

func (walker *callGraphWalker) sortedEdges() (edges []CallGraphEdge, unresolved []CallGraphUnresolvedCall, truncatedSites, truncatedUnresolved bool) {
	edges = make([]CallGraphEdge, 0, len(walker.edges))
	for _, build := range walker.edges {
		edge := build.edge
		edge.CallSites = make([]CallGraphCallSite, 0, len(build.sites))
		for site := range build.sites {
			edge.CallSites = append(edge.CallSites, CallGraphCallSite{Path: site.path, Line: site.line, Column: site.column})
		}
		sort.Slice(edge.CallSites, func(i, j int) bool {
			if edge.CallSites[i].Path != edge.CallSites[j].Path {
				return edge.CallSites[i].Path < edge.CallSites[j].Path
			}
			if edge.CallSites[i].Line != edge.CallSites[j].Line {
				return edge.CallSites[i].Line < edge.CallSites[j].Line
			}
			return edge.CallSites[i].Column < edge.CallSites[j].Column
		})
		edge.ID = reportcontract.Fingerprint("call-graph-edge-v1", edge.Kind, edge.From, edge.To)
		edges = append(edges, edge)
		truncatedSites = truncatedSites || build.truncated
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	unresolved = make([]CallGraphUnresolvedCall, 0, len(walker.unresolved))
	for call := range walker.unresolved {
		unresolved = append(unresolved, call)
	}
	sort.Slice(unresolved, func(i, j int) bool {
		if unresolved[i].Path != unresolved[j].Path {
			return unresolved[i].Path < unresolved[j].Path
		}
		if unresolved[i].Line != unresolved[j].Line {
			return unresolved[i].Line < unresolved[j].Line
		}
		if unresolved[i].Column != unresolved[j].Column {
			return unresolved[i].Column < unresolved[j].Column
		}
		return unresolved[i].Reason < unresolved[j].Reason
	})
	if len(unresolved) > MaximumCallGraphUnresolvedCalls {
		truncatedUnresolved = true
		unresolved = unresolved[:MaximumCallGraphUnresolvedCalls]
	}
	return edges, unresolved, truncatedSites, truncatedUnresolved
}

func countCallGraphEdgeKinds(edges []CallGraphEdge) (static, dispatch int) {
	for _, edge := range edges {
		if edge.Kind == "dispatch" {
			dispatch++
		} else {
			static++
		}
	}
	return static, dispatch
}

func countCallGraphCallSites(edges []CallGraphEdge) int {
	sites := 0
	for _, edge := range edges {
		sites += len(edge.CallSites)
	}
	return sites
}

func finalizeCallGraphSummary(summary *CallGraphSummary, packageCount int, functions []CallGraphFunction, unresolved []CallGraphUnresolvedCall, report Report) {
	summary.Packages = packageCount
	summary.Functions = len(functions)
	summary.UnresolvedCalls = len(unresolved)
	tests, changed := 0, 0
	for _, function := range functions {
		if function.Test {
			tests++
		}
		if function.Changed {
			changed++
		}
	}
	summary.Tests = tests
	summary.ChangedCallables = changed
	summary.UnmatchedCallables = unmatchedGoCallables(report.Methods, functions)
}

func unmatchedGoCallables(methods []MethodResult, functions []CallGraphFunction) int {
	matched := make(map[string]struct{}, len(functions))
	for _, function := range functions {
		matched[function.ID] = struct{}{}
	}
	unmatched := 0
	for _, method := range methods {
		if method.Language == "go" {
			if _, ok := matched[method.ID]; ok {
				continue
			}
			unmatched++
		}
	}
	return unmatched
}

func buildAffectedTests(edges []CallGraphEdge, functions []CallGraphFunction) []CallGraphAffectedTest {
	byID := make(map[string]CallGraphFunction, len(functions))
	reverse := make(map[string][]string)
	for _, edge := range edges {
		reverse[edge.To] = append(reverse[edge.To], edge.From)
	}
	tests := make(map[string]*CallGraphAffectedTest)
	for _, function := range functions {
		byID[function.ID] = function
		if function.Test {
			tests[function.ID] = &CallGraphAffectedTest{Test: function.ID, Name: function.Name, File: function.File, Seeds: make([]string, 0)}
		}
	}
	for _, seed := range sortedChangedSeeds(functions) {
		reachAffectedTests(seed.ID, byID, reverse, tests)
	}
	affected := make([]CallGraphAffectedTest, 0, len(tests))
	for _, test := range tests {
		if len(test.Seeds) == 0 {
			continue
		}
		sort.Strings(test.Seeds)
		affected = append(affected, *test)
	}
	sort.Slice(affected, func(i, j int) bool {
		if affected[i].File != affected[j].File {
			return affected[i].File < affected[j].File
		}
		return affected[i].Name < affected[j].Name
	})
	return affected
}

func sortedChangedSeeds(functions []CallGraphFunction) []CallGraphFunction {
	seeds := make([]CallGraphFunction, 0)
	for _, function := range functions {
		if function.Changed {
			seeds = append(seeds, function)
		}
	}
	sort.Slice(seeds, func(i, j int) bool {
		if seeds[i].File != seeds[j].File {
			return seeds[i].File < seeds[j].File
		}
		if seeds[i].StartLine != seeds[j].StartLine {
			return seeds[i].StartLine < seeds[j].StartLine
		}
		return seeds[i].ID < seeds[j].ID
	})
	if len(seeds) > MaximumCallGraphAffectedTestSeeds {
		seeds = seeds[:MaximumCallGraphAffectedTestSeeds]
	}
	return seeds
}

func reachAffectedTests(seedID string, byID map[string]CallGraphFunction, reverse map[string][]string, tests map[string]*CallGraphAffectedTest) {
	type frontierEntry struct {
		node     string
		distance int
	}
	visited := map[string]int{seedID: 0}
	frontier := []frontierEntry{{node: seedID, distance: 0}}
	for len(frontier) > 0 {
		current := frontier[0]
		frontier = frontier[1:]
		if test, ok := tests[current.node]; ok {
			test.Seeds = append(test.Seeds, seedID)
			if test.Distance == 0 || current.distance < test.Distance {
				test.Distance = current.distance
			}
		}
		for _, caller := range reverse[current.node] {
			if _, seen := visited[caller]; seen {
				continue
			}
			visited[caller] = current.distance + 1
			frontier = append(frontier, frontierEntry{node: caller, distance: current.distance + 1})
		}
	}
}
