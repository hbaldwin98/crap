package analysis

import (
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
)

func newCSharpLanguage() (languageDefinition, error) {
	return languageDefinition{
		name:            "csharp",
		grammarLanguage: "csharp",
		grammarVersion:  "v0.23.5",
		grammar:         treesitter.NewLanguage(tree_sitter_c_sharp.Language()),
		callableKinds: map[string]string{
			"method_declaration":              "method",
			"constructor_declaration":         "constructor",
			"destructor_declaration":          "destructor",
			"operator_declaration":            "operator",
			"conversion_operator_declaration": "conversion_operator",
			"local_function_statement":        "local_function",
			"accessor_declaration":            "accessor",
			"lambda_expression":               "lambda",
			"anonymous_method_expression":     "anonymous_method",
			"property_declaration":            "property",
			"indexer_declaration":             "indexer",
		},
		branchKinds: map[string]bool{
			"if_statement":              true,
			"for_statement":             true,
			"foreach_statement":         true,
			"while_statement":           true,
			"do_statement":              true,
			"catch_clause":              true,
			"conditional_expression":    true,
			"switch_expression_arm":     true,
			"and_pattern":               true,
			"or_pattern":                true,
			"case_switch_label":         true,
			"case_pattern_switch_label": true,
		},
		logicalOps:    map[string]bool{"&&": true, "||": true, "??": true},
		callable:      csharpCallable,
		body:          csharpCallableBody,
		qualifiedName: csharpQualifiedName,
		ownerName:     csharpOwnerName,
	}, nil
}

func csharpCallable(node *treesitter.Node) bool {
	switch node.Kind() {
	case "property_declaration", "indexer_declaration":
		value := node.ChildByFieldName("value")
		return value != nil && value.Kind() == "arrow_expression_clause"
	default:
		return true
	}
}

func csharpCallableBody(node *treesitter.Node) *treesitter.Node {
	if body := node.ChildByFieldName("body"); body != nil {
		return body
	}
	if node.Kind() == "property_declaration" || node.Kind() == "indexer_declaration" {
		return node.ChildByFieldName("value")
	}
	if node.Kind() == "anonymous_method_expression" {
		for index := uint(0); index < node.NamedChildCount(); index++ {
			if child := node.NamedChild(index); child.Kind() == "block" {
				return child
			}
		}
	}
	return nil
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
	// File-scoped namespaces are siblings of their types in this grammar.
	for index := uint(0); index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child.Kind() == "file_scoped_namespace_declaration" {
			if name := child.ChildByFieldName("name"); name != nil {
				parts = append(parts, name.Utf8Text(source))
			}
			break
		}
	}
	reverseStrings(parts)

	callableName := csharpMemberName(node, source)
	if node.Kind() == "lambda_expression" || node.Kind() == "anonymous_method_expression" {
		if assigned := csharpAssignedName(node); assigned != nil {
			callableName = strings.TrimSpace(assigned.Utf8Text(source))
		} else {
			callableName = anonymousCallableName(node)
		}
	}
	if node.Kind() == "accessor_declaration" {
		owner := node.Parent()
		if owner != nil {
			owner = owner.Parent()
		}
		if owner != nil {
			memberName := csharpMemberName(owner, source)
			if memberName != owner.Kind() {
				callableName = memberName + "." + callableName
			}
		}
	}
	parts = append(parts, callableName)
	return strings.Join(parts, ".")
}

func csharpMemberName(node *treesitter.Node, source []byte) string {
	prefix := csharpExplicitInterface(node, source)
	if name := node.ChildByFieldName("name"); name != nil {
		return prefix + name.Utf8Text(source)
	}
	switch node.Kind() {
	case "indexer_declaration":
		return prefix + "this"
	case "operator_declaration":
		if operator := node.ChildByFieldName("operator"); operator != nil {
			return csharpCheckedPrefix(node) + "operator " + operator.Utf8Text(source)
		}
	case "conversion_operator_declaration":
		conversion := "operator"
		for index := uint(0); index < node.ChildCount(); index++ {
			kind := node.Child(index).Kind()
			if kind == "implicit" || kind == "explicit" {
				conversion = kind + " operator"
				break
			}
		}
		if target := node.ChildByFieldName("type"); target != nil {
			return csharpCheckedPrefix(node) + conversion + " " + strings.Join(strings.Fields(target.Utf8Text(source)), "")
		}
	}
	return node.Kind()
}

func csharpExplicitInterface(node *treesitter.Node, source []byte) string {
	for index := uint(0); index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if child.Kind() == "explicit_interface_specifier" {
			value := strings.TrimSpace(child.Utf8Text(source))
			return strings.TrimSuffix(value, ".") + "."
		}
	}
	return ""
}

func csharpCheckedPrefix(node *treesitter.Node) string {
	for index := uint(0); index < node.ChildCount(); index++ {
		if node.Child(index).Kind() == "checked" {
			return "checked "
		}
	}
	return ""
}

func csharpAssignedName(node *treesitter.Node) *treesitter.Node {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "variable_declarator":
			return parent.ChildByFieldName("name")
		case "assignment_expression":
			return parent.ChildByFieldName("left")
		case "parenthesized_expression", "cast_expression":
			continue
		default:
			return nil
		}
	}
	return nil
}

func csharpOwnerName(node *treesitter.Node, source []byte) string {
	switch node.Kind() {
	case "variable_declarator", "public_field_definition":
		return nodeSource(node.ChildByFieldName("name"), source)
	case "pair":
		return nodeSource(node.ChildByFieldName("key"), source)
	case "assignment_expression":
		return nodeSource(node.ChildByFieldName("left"), source)
	case "indexer_declaration":
		parameters := node.ChildByFieldName("parameters")
		return "this" + strings.Join(strings.Fields(nodeSource(parameters, source)), "")
	case "property_declaration", "operator_declaration", "conversion_operator_declaration":
		return csharpMemberName(node, source)
	case "class_declaration", "interface_declaration", "namespace_declaration", "module", "function_declaration", "method_definition":
		return nodeSource(node.ChildByFieldName("name"), source)
	}
	return ""
}
