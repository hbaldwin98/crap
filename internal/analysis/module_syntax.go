package analysis

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
)

func walkNamedContext(ctx context.Context, node *treesitter.Node, visit func(*treesitter.Node) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := visit(node); err != nil {
		return err
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		if err := walkNamedContext(ctx, node.NamedChild(index), visit); err != nil {
			return err
		}
	}
	return nil
}

func referenceLocation(node *treesitter.Node) structuralReference {
	return structuralReference{
		StartLine: int(node.StartPosition().Row) + 1, StartColumn: int(node.StartPosition().Column) + 1,
		EndLine: int(node.EndPosition().Row) + 1, EndColumn: int(node.EndPosition().Column) + 1,
	}
}

func goModuleSyntax(ctx context.Context, root *treesitter.Node, source []byte, relative string) (structuralModuleSyntax, error) {
	result := structuralModuleSyntax{modules: make([]structuralModule, 0, 1), references: make([]structuralReference, 0)}
	packageName := ""
	for index := uint(0); index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child.Kind() == "package_clause" && child.NamedChildCount() > 0 {
			packageName = strings.TrimSpace(child.NamedChild(0).Utf8Text(source))
			break
		}
	}
	directory := filepath.ToSlash(filepath.Dir(relative))
	if directory == "." {
		directory = ""
	}
	result.modules = append(result.modules, structuralModule{System: "go-package", Name: directory, Variant: packageName, Evidence: "package-clause"})
	err := walkNamedContext(ctx, root, func(node *treesitter.Node) error {
		if node.Kind() != "import_spec" {
			return nil
		}
		path := node.ChildByFieldName("path")
		if path == nil {
			return nil
		}
		specifier, err := strconv.Unquote(strings.TrimSpace(path.Utf8Text(source)))
		if err != nil {
			return fmt.Errorf("decode Go import in %s: %w", relative, err)
		}
		binding := "default"
		if name := node.ChildByFieldName("name"); name != nil {
			binding = strings.TrimSpace(name.Utf8Text(source))
		}
		reference := referenceLocation(path)
		reference.Kind, reference.Specifier, reference.Binding = "go-import", specifier, binding
		result.references = append(result.references, reference)
		return nil
	})
	return result, err
}

func typescriptModuleSyntax(ctx context.Context, root *treesitter.Node, source []byte, relative string) (structuralModuleSyntax, error) {
	result := structuralModuleSyntax{
		modules:    []structuralModule{{System: "ecmascript-file", Name: filepath.ToSlash(relative), Evidence: "source-file-module"}},
		references: make([]structuralReference, 0),
	}
	err := walkNamedContext(ctx, root, func(node *treesitter.Node) error {
		kind := ""
		var sourceNode *treesitter.Node
		switch node.Kind() {
		case "import_statement":
			kind = "typescript-import"
			sourceNode = node.ChildByFieldName("source")
			if sourceNode == nil {
				for index := uint(0); index < node.NamedChildCount(); index++ {
					clause := node.NamedChild(index)
					if clause.Kind() == "import_require_clause" {
						kind, sourceNode = "typescript-import-require", clause.ChildByFieldName("source")
						break
					}
				}
			}
			if hasImmediateToken(node, "type") {
				kind = "typescript-import-type"
			}
		case "export_statement":
			sourceNode = node.ChildByFieldName("source")
			if sourceNode != nil {
				kind = "typescript-re-export"
				if hasImmediateToken(node, "type") {
					kind = "typescript-re-export-type"
				}
			}
		}
		if sourceNode == nil || kind == "" {
			return nil
		}
		specifier, reason := decodeTypeScriptSpecifier(sourceNode, source)
		reference := referenceLocation(sourceNode)
		reference.Kind, reference.Specifier, reference.Reason = kind, specifier, reason
		result.references = append(result.references, reference)
		return nil
	})
	return result, err
}

func hasImmediateToken(node *treesitter.Node, kind string) bool {
	for index := uint(0); index < node.ChildCount(); index++ {
		if node.Child(index).Kind() == kind {
			return true
		}
	}
	return false
}

func decodeTypeScriptSpecifier(node *treesitter.Node, source []byte) (string, string) {
	raw := strings.TrimSpace(node.Utf8Text(source))
	if len(raw) < 2 || raw[0] != raw[len(raw)-1] || (raw[0] != '\'' && raw[0] != '"') {
		return raw, "unsupported-specifier"
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		if node.NamedChild(index).Kind() == "escape_sequence" {
			return raw, "unsupported-specifier-escape"
		}
	}
	return raw[1 : len(raw)-1], ""
}

func csharpModuleSyntax(ctx context.Context, root *treesitter.Node, source []byte, _ string) (structuralModuleSyntax, error) {
	result := structuralModuleSyntax{modules: make([]structuralModule, 0), references: make([]structuralReference, 0)}
	if err := collectCSharpModuleSyntax(ctx, root, source, "global::", &result); err != nil {
		return structuralModuleSyntax{}, err
	}
	if len(result.modules) == 0 {
		result.modules = append(result.modules, structuralModule{System: "csharp-namespace", Name: "global::", Evidence: "global-namespace"})
	}
	return result, nil
}

func collectCSharpModuleSyntax(ctx context.Context, node *treesitter.Node, source []byte, scope string, result *structuralModuleSyntax) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	activeScope := scope
	for index := uint(0); index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		switch child.Kind() {
		case "file_scoped_namespace_declaration":
			activeScope = qualifyCSharpNamespace(scope, nodeSource(child.ChildByFieldName("name"), source))
			result.modules = append(result.modules, structuralModule{System: "csharp-namespace", Name: activeScope, Evidence: "namespace-declaration"})
		case "namespace_declaration":
			namespace := qualifyCSharpNamespace(activeScope, nodeSource(child.ChildByFieldName("name"), source))
			result.modules = append(result.modules, structuralModule{System: "csharp-namespace", Name: namespace, Evidence: "namespace-declaration"})
			if err := collectCSharpModuleSyntax(ctx, child, source, namespace, result); err != nil {
				return err
			}
		case "using_directive":
			if reference, ok := csharpUsingReference(child, source, activeScope); ok {
				result.references = append(result.references, reference)
			}
		default:
			if err := collectCSharpModuleSyntax(ctx, child, source, activeScope, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func qualifyCSharpNamespace(scope, name string) string {
	name = strings.Join(strings.Fields(name), "")
	if strings.HasPrefix(name, "global::") || scope == "global::" {
		return strings.TrimPrefix(name, "global::")
	}
	return strings.TrimSuffix(scope, ".") + "." + name
}

func csharpUsingReference(node *treesitter.Node, source []byte, scope string) (structuralReference, bool) {
	alias := node.ChildByFieldName("name")
	var target *treesitter.Node
	for index := uint(0); index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if alias == nil || child.StartByte() != alias.StartByte() || child.EndByte() != alias.EndByte() {
			target = child
		}
	}
	if target == nil {
		return structuralReference{}, false
	}
	reference := referenceLocation(target)
	reference.Kind, reference.Scope = "csharp-using", scope
	reference.Specifier = strings.Join(strings.Fields(target.Utf8Text(source)), "")
	if alias != nil {
		reference.Kind, reference.Binding = "csharp-using-alias", strings.TrimSpace(alias.Utf8Text(source))
	} else if hasImmediateToken(node, "static") {
		reference.Kind = "csharp-using-static"
	}
	return reference, true
}
