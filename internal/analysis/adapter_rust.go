package analysis

import (
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

func newRustLanguage() (languageDefinition, error) {
	return languageDefinition{
		name:            "rust",
		grammarLanguage: "rust",
		grammarVersion:  "v0.24.0",
		grammar:         treesitter.NewLanguage(tree_sitter_rust.Language()),
		callableKinds: map[string]string{
			"function_item":      "function",
			"closure_expression": "closure",
		},
		typeKinds: map[string]string{
			"struct_item": "struct", "enum_item": "enum", "union_item": "union",
			"trait_item": "trait", "type_item": "type_alias",
		},
		branchKinds: map[string]bool{
			"if_expression":    true,
			"while_expression": true,
			"for_expression":   true,
		},
		logicalOps:    map[string]bool{"&&": true, "||": true},
		extraBranch:   rustExtraBranch,
		qualifiedName: rustQualifiedName,
		qualifiedType: rustQualifiedTypeName,
		ownerName:     rustOwnerName,
		moduleSyntax:  rustModuleSyntax,
	}, nil
}

// rustExtraBranch counts match arms other than the irrefutable catch-all, plus
// the `?` operator, which introduces an implicit early return.
func rustExtraBranch(node *treesitter.Node, source []byte) bool {
	switch node.Kind() {
	case "match_arm":
		return !rustCatchAllArm(node, source)
	case "try_expression":
		return true
	}
	return false
}

func rustCatchAllArm(node *treesitter.Node, source []byte) bool {
	pattern := node.ChildByFieldName("pattern")
	if pattern == nil {
		return false
	}
	return strings.TrimSpace(pattern.Utf8Text(source)) == "_"
}

func rustQualifiedName(node *treesitter.Node, source []byte) string {
	name := nodeSource(node.ChildByFieldName("name"), source)
	if name == "" {
		if assigned := rustAssignedName(node); assigned != nil {
			name = strings.TrimSpace(assigned.Utf8Text(source))
		}
	}
	if name == "" {
		name = anonymousCallableName(node)
	}
	return strings.Join(append(rustScopeParts(node, source), name), "::")
}

func rustQualifiedTypeName(node *treesitter.Node, source []byte) string {
	name := nodeSource(node.ChildByFieldName("name"), source)
	return strings.Join(append(rustScopeParts(node, source), name), "::")
}

// rustScopeParts walks outward collecting enclosing `mod` and `trait` names and
// the type an `impl` block applies to, so callables carry a path-like identity.
func rustScopeParts(node *treesitter.Node, source []byte) []string {
	parts := make([]string, 0, 3)
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "mod_item":
			parts = append(parts, nodeSource(parent.ChildByFieldName("name"), source))
		case "trait_item":
			parts = append(parts, nodeSource(parent.ChildByFieldName("name"), source))
		case "impl_item":
			parts = append(parts, rustImplName(parent, source))
		}
	}
	reverseStrings(parts)
	filtered := parts[:0]
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func rustImplName(node *treesitter.Node, source []byte) string {
	target := nodeSource(node.ChildByFieldName("type"), source)
	target = strings.Join(strings.Fields(target), "")
	if trait := node.ChildByFieldName("trait"); trait != nil {
		traitName := strings.Join(strings.Fields(trait.Utf8Text(source)), "")
		return "<" + target + " as " + traitName + ">"
	}
	return target
}

// rustAssignedName names a closure after the binding it is stored in, matching
// how the Go and TypeScript adapters name function literals.
func rustAssignedName(node *treesitter.Node) *treesitter.Node {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "let_declaration":
			return parent.ChildByFieldName("pattern")
		case "static_item", "const_item", "field_declaration":
			return parent.ChildByFieldName("name")
		case "assignment_expression":
			return parent.ChildByFieldName("left")
		case "parenthesized_expression", "reference_expression", "type_cast_expression":
			continue
		default:
			return nil
		}
	}
	return nil
}

func rustOwnerName(node *treesitter.Node, source []byte) string {
	switch node.Kind() {
	case "let_declaration":
		return nodeSource(node.ChildByFieldName("pattern"), source)
	case "assignment_expression":
		return nodeSource(node.ChildByFieldName("left"), source)
	case "function_item", "function_signature_item", "struct_item", "enum_item", "union_item",
		"trait_item", "type_item", "mod_item", "static_item", "const_item", "field_declaration":
		return nodeSource(node.ChildByFieldName("name"), source)
	case "impl_item":
		return rustImplName(node, source)
	}
	return ""
}
