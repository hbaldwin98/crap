package analysis

import (
	"fmt"
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func newTypeScriptLanguage(tsx bool) (languageDefinition, error) {
	grammar := tree_sitter_typescript.LanguageTypescript()
	grammarLanguage := "typescript"
	if tsx {
		grammar = tree_sitter_typescript.LanguageTSX()
		grammarLanguage = "tsx"
	}
	return languageDefinition{
		name:            "typescript",
		grammarLanguage: grammarLanguage,
		grammarVersion:  "v0.23.2",
		grammar:         treesitter.NewLanguage(grammar),
		callableKinds: map[string]string{
			"function_declaration":           "function",
			"generator_function_declaration": "generator_function",
			"function_expression":            "function",
			"generator_function":             "generator_function",
			"arrow_function":                 "arrow_function",
			"method_definition":              "method",
		},
		typeKinds: map[string]string{
			"class_declaration": "class", "abstract_class_declaration": "class", "interface_declaration": "interface",
			"type_alias_declaration": "type_alias", "enum_declaration": "enum",
		},
		branchKinds: map[string]bool{
			"if_statement":       true,
			"for_statement":      true,
			"for_in_statement":   true,
			"while_statement":    true,
			"do_statement":       true,
			"catch_clause":       true,
			"switch_case":        true,
			"ternary_expression": true,
		},
		logicalOps:    map[string]bool{"&&": true, "||": true, "??": true},
		qualifiedName: typescriptQualifiedName,
		qualifiedType: typescriptQualifiedTypeName,
		ownerName:     typescriptOwnerName,
		moduleSyntax:  typescriptModuleSyntax,
	}, nil
}

func typescriptQualifiedTypeName(node *treesitter.Node, source []byte) string {
	parts := make([]string, 0)
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "class_declaration", "abstract_class_declaration", "internal_module", "module":
			if name := parent.ChildByFieldName("name"); name != nil {
				parts = append(parts, name.Utf8Text(source))
			}
		}
	}
	reverseStrings(parts)
	parts = append(parts, nodeSource(node.ChildByFieldName("name"), source))
	return strings.Join(parts, ".")
}

func typescriptQualifiedName(node *treesitter.Node, source []byte) string {
	name := node.ChildByFieldName("name")
	if name == nil {
		name = typescriptAssignedName(node)
	}
	callableName := anonymousCallableName(node)
	if name != nil {
		callableName = strings.TrimSpace(name.Utf8Text(source))
	}

	parts := make([]string, 0, 3)
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "class_declaration", "abstract_class_declaration", "internal_module", "module":
			if parentName := parent.ChildByFieldName("name"); parentName != nil {
				parts = append(parts, parentName.Utf8Text(source))
			}
		}
	}
	reverseStrings(parts)
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

func typescriptOwnerName(node *treesitter.Node, source []byte) string {
	switch node.Kind() {
	case "variable_declarator", "public_field_definition":
		return nodeSource(node.ChildByFieldName("name"), source)
	case "pair":
		return nodeSource(node.ChildByFieldName("key"), source)
	case "assignment_expression":
		return nodeSource(node.ChildByFieldName("left"), source)
	case "class_declaration", "abstract_class_declaration", "interface_declaration", "type_alias_declaration", "enum_declaration", "namespace_declaration", "module", "function_declaration", "method_definition":
		return nodeSource(node.ChildByFieldName("name"), source)
	}
	return ""
}

func anonymousCallableName(node *treesitter.Node) string {
	position := node.StartPosition()
	return fmt.Sprintf("<anonymous@%d:%d>", position.Row+1, position.Column+1)
}

func reverseStrings(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
