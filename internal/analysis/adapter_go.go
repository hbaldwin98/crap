package analysis

import (
	"strings"

	treesitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

func newGoLanguage() (languageDefinition, error) {
	return languageDefinition{
		name:            "go",
		grammarLanguage: "go",
		grammarVersion:  "v0.23.4",
		grammar:         treesitter.NewLanguage(tree_sitter_go.Language()),
		callableKinds: map[string]string{
			"function_declaration": "function",
			"method_declaration":   "method",
			"func_literal":         "function_literal",
		},
		branchKinds: map[string]bool{
			"if_statement":  true,
			"for_statement": true,
		},
		logicalOps:    map[string]bool{"&&": true, "||": true},
		extraBranch:   goCaseBranch,
		qualifiedName: goQualifiedName,
		ownerName:     goOwnerName,
	}, nil
}

func goCaseBranch(node *treesitter.Node, source []byte) bool {
	if node.Kind() != "expression_case" && node.Kind() != "type_case" && node.Kind() != "communication_case" {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(node.Utf8Text(source)), "default")
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
		var receiverType *treesitter.Node
		if receiver.NamedChildCount() > 0 {
			receiverType = receiver.NamedChild(0).ChildByFieldName("type")
		}
		if receiverType != nil {
			parts = append(parts, "("+strings.Join(strings.Fields(receiverType.Utf8Text(source)), "")+")")
		}
	}
	name := node.ChildByFieldName("name")
	if name == nil && node.Kind() == "func_literal" {
		name = goAssignedName(node)
	}
	if name != nil {
		parts = append(parts, strings.TrimSpace(name.Utf8Text(source)))
	} else {
		parts = append(parts, anonymousCallableName(node))
	}
	return strings.Join(parts, ".")
}

func goAssignedName(node *treesitter.Node) *treesitter.Node {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		switch parent.Kind() {
		case "expression_list":
			if parent.NamedChildCount() != 1 {
				return nil
			}
			continue
		case "parenthesized_expression":
			continue
		case "var_spec":
			return parent.ChildByFieldName("name")
		case "short_var_declaration", "assignment_statement":
			left := parent.ChildByFieldName("left")
			if left != nil && left.NamedChildCount() == 1 {
				return left.NamedChild(0)
			}
			return nil
		default:
			return nil
		}
	}
	return nil
}

func goOwnerName(node *treesitter.Node, source []byte) string {
	switch node.Kind() {
	case "var_spec":
		return nodeSource(node.ChildByFieldName("name"), source)
	case "short_var_declaration", "assignment_statement":
		return nodeSource(node.ChildByFieldName("left"), source)
	case "function_declaration", "method_declaration":
		return nodeSource(node.ChildByFieldName("name"), source)
	}
	return ""
}
