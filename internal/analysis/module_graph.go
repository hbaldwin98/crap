package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hbaldwin98/crap/internal/reportcontract"
)

type codeGraphModuleBuild struct {
	node  CodeGraphNode
	files map[string]bool
}

func buildModuleGraph(ctx context.Context, graph *CodeGraphReport, inputs analysisInputs) error {
	goModulePath, resolutionInput, err := loadCodeGraphGoModule(inputs.root)
	if err != nil {
		return err
	}
	if resolutionInput != nil {
		graph.ResolutionInputs = append(graph.ResolutionInputs, *resolutionInput)
		baseConfig := graph.Fingerprints.ConfigSHA256
		graph.Fingerprints.ConfigSHA256 = reportcontract.JSONFingerprint(struct {
			Base   string                           `json:"base"`
			Inputs []reportcontract.FileFingerprint `json:"inputs"`
		}{baseConfig, graph.ResolutionInputs})
	}

	fileIDs := indexFileIDs(graph)
	modules, fileModules, err := collectCodeGraphModules(ctx, graph, inputs, goModulePath)
	if err != nil {
		return err
	}
	appendModuleNodesAndMemberEdges(graph, modules, fileIDs)
	dependencyReferences, err := collectModuleReferences(ctx, graph, inputs, fileModules, fileIDs, modules)
	if err != nil {
		return err
	}
	appendModuleImportEdges(graph, dependencyReferences)
	aggregateCodeGraphModuleMetrics(graph, modules, fileModules)
	return nil
}

func indexFileIDs(graph *CodeGraphReport) map[string]string {
	fileIDs := make(map[string]string)
	for _, node := range graph.Nodes {
		if node.Kind == "file" {
			fileIDs[node.Path] = node.ID
		}
	}
	return fileIDs
}

func collectCodeGraphModules(ctx context.Context, graph *CodeGraphReport, inputs analysisInputs, goModulePath string) (map[string]*codeGraphModuleBuild, map[string][]string, error) {
	modules := make(map[string]*codeGraphModuleBuild)
	fileModules := make(map[string][]string)
	for _, source := range graph.Fingerprints.Sources {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		structure := inputs.structures[source.Path]
		for _, declaration := range structure.modules {
			identity := canonicalModuleIdentity(declaration, goModulePath)
			id := reportcontract.Fingerprint("code-graph-module-v1", structure.language, identity.System, identity.Name, identity.Variant)
			module := modules[id]
			if module == nil {
				module = &codeGraphModuleBuild{
					node:  CodeGraphNode{ID: id, Kind: "module", Language: structure.language, Name: identity.Name, Path: source.Path, Module: &identity},
					files: make(map[string]bool),
				}
				modules[id] = module
			}
			module.files[source.Path] = true
			fileModules[source.Path] = appendUniqueString(fileModules[source.Path], id)
		}
	}
	return modules, fileModules, nil
}

func appendModuleNodesAndMemberEdges(graph *CodeGraphReport, modules map[string]*codeGraphModuleBuild, fileIDs map[string]string) {
	for _, id := range sortedModuleIDs(modules) {
		module := modules[id]
		graph.Nodes = append(graph.Nodes, module.node)
		files := sortedBoolKeys(module.files)
		for _, path := range files {
			graph.Edges = append(graph.Edges, newModuleGraphEdge("member-of", fileIDs[path], id, "module-declaration", nil))
		}
	}
}

func collectModuleReferences(ctx context.Context, graph *CodeGraphReport, inputs analysisInputs, fileModules map[string][]string, fileIDs map[string]string, modules map[string]*codeGraphModuleBuild) (map[string][]string, error) {
	moduleByName := indexModulesByName(modules)
	occurrences := make(map[string]int)
	dependencyReferences := make(map[string][]string)
	for _, source := range graph.Fingerprints.Sources {
		structure := inputs.structures[source.Path]
		sourceModules := append([]string(nil), fileModules[source.Path]...)
		sort.Strings(sourceModules)
		for _, raw := range structure.references {
			for _, sourceModule := range sourceModules {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				key := strings.Join([]string{fileIDs[source.Path], sourceModule, raw.Kind, raw.Specifier, raw.Scope, raw.Binding}, "\x00")
				occurrence := occurrences[key]
				occurrences[key]++
				reference := resolveCodeGraphReference(raw, structure.language, source.Path, fileIDs[source.Path], sourceModule, occurrence, modules, moduleByName)
				graph.References = append(graph.References, reference)
				if reference.Resolution == "resolved" {
					edgeKey := sourceModule + "\x00" + reference.Target
					dependencyReferences[edgeKey] = append(dependencyReferences[edgeKey], reference.ID)
				}
			}
		}
	}
	return dependencyReferences, nil
}

func appendModuleImportEdges(graph *CodeGraphReport, dependencyReferences map[string][]string) {
	dependencyKeys := make([]string, 0, len(dependencyReferences))
	for key := range dependencyReferences {
		dependencyKeys = append(dependencyKeys, key)
	}
	sort.Strings(dependencyKeys)
	for _, key := range dependencyKeys {
		parts := strings.SplitN(key, "\x00", 2)
		references := dependencyReferences[key]
		sort.Strings(references)
		graph.Edges = append(graph.Edges, newModuleGraphEdge("imports", parts[0], parts[1], "static-import", references))
	}
}

func loadCodeGraphGoModule(root string) (string, *reportcontract.FileFingerprint, error) {
	path := filepath.Join(root, "go.mod")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("inspect go.mod: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("go.mod must be a regular file and not a symlink")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read go.mod: %w", err)
	}
	fingerprint := reportcontract.FileFingerprint{Path: "go.mod", SHA256: reportcontract.SHA256(data)}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "module "))
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		return strings.TrimSpace(value), &fingerprint, nil
	}
	return "", &fingerprint, nil
}

func canonicalModuleIdentity(module structuralModule, goModulePath string) CodeGraphModuleIdentity {
	name := module.Name
	if module.System == "go-package" {
		if goModulePath != "" {
			name = strings.TrimSuffix(goModulePath, "/")
			if module.Name != "" {
				name += "/" + strings.TrimPrefix(module.Name, "/")
			}
		} else if name == "" {
			name = "."
		} else {
			name = "./" + name
		}
	}
	return CodeGraphModuleIdentity{System: module.System, Name: name, Variant: module.Variant}
}

func resolveCodeGraphReference(raw structuralReference, language, path, fileID, sourceModule string, occurrence int, modules map[string]*codeGraphModuleBuild, moduleByName map[string][]string) CodeGraphReference {
	reference := CodeGraphReference{
		ID:   reportcontract.Fingerprint("code-graph-reference-v1", fileID, sourceModule, raw.Kind, raw.Specifier, raw.Scope, raw.Binding, strconv.Itoa(occurrence)),
		Kind: raw.Kind, Language: language, SourceFile: fileID, SourceModule: sourceModule, Specifier: raw.Specifier, Scope: raw.Scope, Binding: raw.Binding,
		Location:   CodeGraphLocation{StartLine: raw.StartLine, StartColumn: raw.StartColumn, EndLine: raw.EndLine, EndColumn: raw.EndColumn},
		Resolution: "unresolved", Candidates: make([]string, 0), Reason: raw.Reason,
	}
	if reference.Reason != "" {
		return reference
	}
	candidates, reason := codeGraphReferenceCandidates(raw, path, modules, moduleByName)
	for _, candidate := range candidates {
		if candidate != sourceModule {
			reference.Candidates = append(reference.Candidates, candidate)
		}
	}
	sort.Strings(reference.Candidates)
	reference.Candidates = compactStrings(reference.Candidates)
	switch len(reference.Candidates) {
	case 0:
		reference.Reason = reason
		if reference.Reason == "" {
			reference.Reason = "outside-selected-sources"
		}
	case 1:
		reference.Resolution, reference.Target, reference.Reason = "resolved", reference.Candidates[0], ""
	default:
		reference.Resolution, reference.Reason = "ambiguous", "multiple-candidates"
	}
	return reference
}

func codeGraphReferenceCandidates(raw structuralReference, sourcePath string, modules map[string]*codeGraphModuleBuild, moduleByName map[string][]string) ([]string, string) {
	switch {
	case raw.Kind == "go-import":
		if raw.Specifier == "C" {
			return nil, "cgo-pseudo-package"
		}
		return eligibleGoModules(moduleByName[raw.Specifier], modules), "outside-selected-sources"
	case strings.HasPrefix(raw.Kind, "typescript-"):
		return typeScriptModuleCandidates(raw.Specifier, sourcePath, moduleByName)
	case strings.HasPrefix(raw.Kind, "rust-"):
		return rustModuleCandidates(raw, sourcePath, moduleByName)
	case strings.HasPrefix(raw.Kind, "csharp-"):
		return csharpModuleCandidates(raw, moduleByName)
	default:
		return nil, "unsupported-reference-kind"
	}
}

func eligibleGoModules(ids []string, modules map[string]*codeGraphModuleBuild) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		module := modules[id].node.Module
		if module != nil && module.System == "go-package" && !strings.HasSuffix(module.Variant, "_test") {
			result = append(result, id)
		}
	}
	return result
}

func typeScriptModuleCandidates(specifier, sourcePath string, moduleByName map[string][]string) ([]string, string) {
	if !strings.HasPrefix(specifier, "./") && !strings.HasPrefix(specifier, "../") {
		return nil, "non-relative-specifier"
	}
	base := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(specifier))))
	if base == ".." || strings.HasPrefix(base, "../") {
		return nil, "path-escapes-root"
	}
	candidatePaths := make([]string, 0, 8)
	extension := strings.ToLower(filepath.Ext(base))
	switch extension {
	case ".ts", ".tsx":
		candidatePaths = append(candidatePaths, base)
	case ".js", ".jsx":
		stem := strings.TrimSuffix(base, extension)
		candidatePaths = append(candidatePaths, stem+".ts", stem+".tsx", stem+".d.ts")
	case "":
		candidatePaths = append(candidatePaths, base+".ts", base+".tsx", base+".d.ts", base+"/index.ts", base+"/index.tsx", base+"/index.d.ts")
	default:
		return nil, "unsupported-extension"
	}
	result := make([]string, 0)
	for _, candidate := range candidatePaths {
		result = append(result, moduleByName[filepath.ToSlash(candidate)]...)
	}
	return result, "outside-selected-sources"
}

// rustModuleCandidates resolves a crate-relative path to the longest module
// prefix that names a selected source module, because a `use` path usually ends
// in an item rather than a module.
func rustModuleCandidates(raw structuralReference, sourcePath string, moduleByName map[string][]string) ([]string, string) {
	specifier, reason := rustAbsoluteSpecifier(raw, sourcePath)
	if reason != "" {
		return nil, reason
	}
	segments := strings.Split(specifier, "::")
	for length := len(segments); length > 0; length-- {
		if candidates := moduleByName[strings.Join(segments[:length], "::")]; len(candidates) > 0 {
			return candidates, ""
		}
	}
	return nil, "outside-selected-sources"
}

// rustAbsoluteSpecifier rewrites `self::`, `super::`, and bare relative paths
// against the module that declared them. External crate paths are not resolved.
func rustAbsoluteSpecifier(raw structuralReference, sourcePath string) (string, string) {
	if raw.Kind == "rust-mod" {
		return raw.Specifier, ""
	}
	current := strings.Split(rustModulePath(sourcePath), "::")
	segments := strings.Split(raw.Specifier, "::")
	switch segments[0] {
	case "crate":
		return raw.Specifier, ""
	case "self":
		return strings.Join(append(current, segments[1:]...), "::"), ""
	case "super":
		for len(segments) > 0 && segments[0] == "super" {
			if len(current) <= 1 {
				return "", "path-escapes-crate-root"
			}
			current, segments = current[:len(current)-1], segments[1:]
		}
		return strings.Join(append(current, segments...), "::"), ""
	default:
		return "", "non-crate-specifier"
	}
}

func csharpModuleCandidates(raw structuralReference, moduleByName map[string][]string) ([]string, string) {
	specifier := strings.TrimPrefix(raw.Specifier, "global::")
	names := []string{specifier}
	if raw.Scope != "" && raw.Scope != "global::" && !strings.HasPrefix(raw.Specifier, "global::") {
		parts := strings.Split(raw.Scope, ".")
		for index := len(parts); index > 0; index-- {
			names = append([]string{strings.Join(parts[:index], ".") + "." + specifier}, names...)
		}
	}
	result := make([]string, 0)
	for _, name := range names {
		result = append(result, moduleByName[name]...)
		if len(result) > 0 {
			break
		}
	}
	return result, "namespace-target-not-found"
}

func indexModulesByName(modules map[string]*codeGraphModuleBuild) map[string][]string {
	result := make(map[string][]string)
	for _, id := range sortedModuleIDs(modules) {
		name := modules[id].node.Module.Name
		result[name] = append(result[name], id)
	}
	return result
}

func aggregateCodeGraphModuleMetrics(graph *CodeGraphReport, modules map[string]*codeGraphModuleBuild, fileModules map[string][]string) {
	nodesByModule := make(map[string][]CodeGraphNode)
	for _, node := range graph.Nodes {
		if node.Kind == "module" {
			continue
		}
		for _, moduleID := range fileModules[node.Path] {
			nodesByModule[moduleID] = append(nodesByModule[moduleID], node)
		}
	}
	for index := range graph.Nodes {
		node := &graph.Nodes[index]
		if node.Kind != "module" {
			continue
		}
		metrics := CodeGraphModuleMetrics{Files: len(modules[node.ID].files)}
		coverageTotal := 0.0
		for _, child := range nodesByModule[node.ID] {
			switch child.Kind {
			case "type":
				metrics.Types++
			case "callable":
				metrics.Callables++
				metrics.ComplexityTotal += child.Metrics.Complexity
				metrics.ComplexityMaximum = max(metrics.ComplexityMaximum, child.Metrics.Complexity)
				metrics.CRAPMaximum = max(metrics.CRAPMaximum, child.Metrics.CRAP)
				if child.Metrics.AboveThreshold {
					metrics.AboveThreshold++
				}
				if child.Metrics.CoveragePercent == nil {
					metrics.CoverageUnknown++
				} else {
					metrics.CoverageKnown++
					coverageTotal += *child.Metrics.CoveragePercent
				}
			}
		}
		if metrics.CoverageKnown > 0 {
			mean := round(coverageTotal/float64(metrics.CoverageKnown), 2)
			metrics.CoverageMean = &mean
		}
		node.ModuleMetrics = &metrics
	}
}

func newModuleGraphEdge(edgeType, from, to, evidence string, references []string) CodeGraphEdge {
	refs := append(make([]string, 0, len(references)), references...)
	return CodeGraphEdge{
		ID: reportcontract.Fingerprint("code-graph-edge-v1", edgeType, from, to), Type: edgeType, From: from, To: to,
		Resolution: "exact", Evidence: evidence, Occurrences: max(1, len(refs)), References: refs,
	}
}

func sortedModuleIDs(modules map[string]*codeGraphModuleBuild) []string {
	ids := make([]string, 0, len(modules))
	for id := range modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
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
