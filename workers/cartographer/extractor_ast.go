package cartographer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// extIndex is built once at init time for O(1) extension lookup.
var extIndex = buildExtensionIndex(astLanguageRegistry)

// TreeSitterASTExtractor extracts type/function declarations using tree-sitter.
// Supports Go, Python, JavaScript, TypeScript, TSX, Rust, Java, Ruby, C, C++, C#.
type TreeSitterASTExtractor struct{}

// NewTreeSitterASTExtractor returns an initialised TreeSitterASTExtractor.
func NewTreeSitterASTExtractor() *TreeSitterASTExtractor {
	return &TreeSitterASTExtractor{}
}

func (e *TreeSitterASTExtractor) Name() string { return "ast" }

func (e *TreeSitterASTExtractor) Matches(path string) bool {
	ext := strings.ToLower(fileExt(path))
	_, ok := extIndex[ext]
	return ok
}

func (e *TreeSitterASTExtractor) Extract(ctx context.Context, src Source) ([]Draft, error) {
	ext := strings.ToLower(src.Extension)
	handler, ok := extIndex[ext]
	if !ok {
		return nil, nil
	}

	parser := sitter.NewParser()
	parser.SetLanguage(handler.language)

	tree, err := parser.ParseCtx(ctx, nil, src.Content)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter parse (%s): %w", handler.name, err)
	}
	defer tree.Close()

	root := tree.RootNode()
	srcHash := hashBytes(src.Content)

	// Go uses a hand-written walker that also extracts doc comments.
	if handler.name == "go" {
		return extractGoDeclarations(root, src, srcHash), nil
	}

	return extractViaQuery(handler, root, src, srcHash)
}

// packageTag returns a "pkg-<dir>" tag derived from the source file's parent directory.
// For example, "ingress/cli/bootstrap.go" → "pkg-cmd".
func packageTag(srcPath string) string {
	dir := filepath.Dir(srcPath)
	base := filepath.Base(dir)
	if base == "." || base == "/" {
		return ""
	}
	// Sanitize: replace dots/underscores with hyphens for kebab-case.
	base = strings.ToLower(base)
	base = strings.NewReplacer(".", "-", "_", "-").Replace(base)
	return "pkg-" + base
}

// buildTags constructs the tag list for an AST draft, adding semantic tags
// beyond the base [language, kind, "ast", "bootstrap"] set.
func buildTags(lang, kind, srcPath, symbolName string, extras ...string) []string {
	tags := []string{lang, kind, "ast", "bootstrap"}

	// Package tag from source path.
	if pkg := packageTag(srcPath); pkg != "" {
		tags = append(tags, pkg)
	}

	// Test tag for Go/Python test functions.
	if strings.HasPrefix(symbolName, "Test") || strings.HasPrefix(symbolName, "test_") {
		tags = append(tags, "test")
	}

	// Append any extras (e.g., receiver tag for methods).
	tags = append(tags, extras...)

	return tags
}

// extractReceiverType extracts the type name from a Go receiver string like "(s *Server)" → "server".
func extractReceiverType(recv string) string {
	// Strip parentheses.
	recv = strings.TrimSpace(recv)
	recv = strings.TrimPrefix(recv, "(")
	recv = strings.TrimSuffix(recv, ")")
	// Split on space to get the type part.
	parts := strings.Fields(recv)
	if len(parts) == 0 {
		return ""
	}
	// Take the last part (the type), strip pointer.
	typeName := parts[len(parts)-1]
	typeName = strings.TrimPrefix(typeName, "*")
	return strings.ToLower(typeName)
}

// extractViaQuery uses a tree-sitter Scheme query to extract named symbols.
func extractViaQuery(h *languageHandler, root *sitter.Node, src Source, srcHash string) ([]Draft, error) {
	q, err := sitter.NewQuery([]byte(h.query), h.language)
	if err != nil {
		return nil, fmt.Errorf("ast query (%s): %w", h.name, err)
	}
	defer q.Close()

	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, root)

	lines := linesOf(src.Content)

	var drafts []Draft
	seen := make(map[string]bool) // deduplicate (same name, same line)

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		for _, cap := range m.Captures {
			node := cap.Node
			name := node.Content(src.Content)
			if name == "" {
				continue
			}

			line := int(node.StartPoint().Row) + 1
			endLine := int(node.EndPoint().Row) + 1

			dedupeKey := fmt.Sprintf("%s:%d", name, line)
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true

			kind := symbolKind(h.name, node)

			// Resolve the definition node (parent of the name node) for doc extraction.
			defNode := node.Parent()

			// For export_statement wrapping (e.g. TypeScript/JavaScript), step up one more level
			// so that leading-comment extraction looks above the export keyword.
			if defNode != nil && defNode.Parent() != nil && defNode.Parent().Type() == "export_statement" {
				defNode = defNode.Parent()
			}

			doc := extractDoc(h.name, defNode, src.Content, lines)

			content := map[string]any{
				"symbol":   name,
				"kind":     kind,
				"language": h.name,
			}
			if doc != "" {
				content["doc"] = doc
			}

			summary := fmt.Sprintf("%s %s: %s", h.name, kind, name)
			if doc != "" {
				summary = fmt.Sprintf("%s %s: %s — %s", h.name, kind, name, truncate(doc, 100))
			}

			drafts = append(drafts, Draft{
				Summary: summary,
				Tags:    buildTags(h.name, kind, src.Path, name),
				Content: content,
				Provenance: ProvenanceMeta{
					SourceFile:   src.Path,
					SourceLines:  lineRange(line, endLine),
					SourceHash:   srcHash,
					TriggerEvent: TriggerEvent,
				},
			})
		}
	}

	return drafts, nil
}

// extractDoc returns documentation for a definition node.
// For Python function_definition and class_definition it reads the inline docstring.
// For all other languages it reads the leading // or /* comment block above the node.
func extractDoc(lang string, defNode *sitter.Node, src []byte, lines []string) string {
	if defNode == nil {
		return ""
	}
	if lang == "python" {
		nodeType := defNode.Type()
		if nodeType == "function_definition" || nodeType == "class_definition" {
			return extractPythonDocstring(defNode, src)
		}
		return ""
	}
	// Generic: extract leading comment lines above the definition node.
	return extractLeadingComment(defNode, lines)
}

// extractPythonDocstring extracts the first string literal from the body of a
// Python function_definition or class_definition node.
func extractPythonDocstring(defNode *sitter.Node, src []byte) string {
	for i := 0; i < int(defNode.ChildCount()); i++ {
		child := defNode.Child(i)
		if child == nil || child.Type() != "block" {
			continue
		}
		if child.ChildCount() == 0 {
			return ""
		}
		first := child.Child(0)
		if first == nil || first.Type() != "expression_statement" {
			return ""
		}
		if first.ChildCount() == 0 {
			return ""
		}
		strNode := first.Child(0)
		if strNode == nil || strNode.Type() != "string" {
			return ""
		}
		// Prefer the string_content child (avoids including quote characters).
		for j := 0; j < int(strNode.ChildCount()); j++ {
			sc := strNode.Child(j)
			if sc != nil && sc.Type() == "string_content" {
				return strings.TrimSpace(sc.Content(src))
			}
		}
		// Fallback: strip surrounding quote characters manually.
		raw := strings.TrimSpace(strNode.Content(src))
		for _, q := range []string{`"""`, `'''`, `"`, `'`} {
			if strings.HasPrefix(raw, q) && strings.HasSuffix(raw, q) && len(raw) >= 2*len(q) {
				return strings.TrimSpace(raw[len(q) : len(raw)-len(q)])
			}
		}
		return raw
	}
	return ""
}

// symbolKind maps a captured node's parent node type to a human-readable kind string.
func symbolKind(lang string, node *sitter.Node) string {
	parent := node.Parent()
	if parent == nil {
		return "symbol"
	}

	// Python: function_definition inside a class body → method.
	if lang == "python" && parent.Type() == "function_definition" {
		if isInsidePythonClass(parent) {
			return "method"
		}
		return "function"
	}

	return nodeTypeToKind(lang, parent.Type())
}

// isInsidePythonClass reports whether a Python function_definition node is
// directly contained in a class body (block whose parent is class_definition).
func isInsidePythonClass(funcNode *sitter.Node) bool {
	block := funcNode.Parent()
	if block == nil {
		return false
	}
	classNode := block.Parent()
	return classNode != nil && classNode.Type() == "class_definition"
}

// nodeTypeToKind converts a tree-sitter node type string to a canonical kind label.
func nodeTypeToKind(lang, nodeType string) string {
	switch nodeType {
	// Shared across many languages
	case "class_declaration", "class_definition", "class_specifier", "class":
		return "class"
	case "function_declaration", "function_definition", "function_item":
		return "function"
	case "method_declaration", "method_definition", "singleton_method":
		return "method"
	case "interface_declaration":
		return "interface"
	case "enum_declaration", "enum_item":
		return "enum"
	case "struct_specifier", "struct_item":
		return "struct"
	case "trait_item":
		return "trait"
	case "impl_item":
		return "impl"
	case "module":
		return "module"
	case "record_declaration":
		return "record"
	case "type_definition":
		return "typedef"
	case "lexical_declaration", "variable_declaration":
		return "const"
	default:
		// Sanitise tree-sitter node types (e.g. "variable_declarator") to
		// kebab-case so they are valid memory tags.
		return strings.ReplaceAll(nodeType, "_", "-")
	}
}

// extractGoDeclarations walks a Go AST and extracts top-level declarations.
func extractGoDeclarations(root *sitter.Node, src Source, srcHash string) []Draft {
	var drafts []Draft
	lines := linesOf(src.Content)

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child == nil {
			continue
		}

		switch child.Type() {
		case "type_declaration":
			drafts = append(drafts, goTypeDraft(child, src, srcHash, lines)...)
		case "function_declaration":
			d := goFuncDraft(child, src, srcHash, lines)
			if d != nil {
				drafts = append(drafts, *d)
			}
		case "method_declaration":
			d := goMethodDraft(child, src, srcHash, lines)
			if d != nil {
				drafts = append(drafts, *d)
			}
		}
	}
	return drafts
}

// goTypeDraft produces a fact draft for a top-level Go type declaration.
func goTypeDraft(node *sitter.Node, src Source, srcHash string, lines []string) []Draft {
	startLine := int(node.StartPoint().Row)
	endLine := int(node.EndPoint().Row)

	// Find the type name.
	name := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "type_spec" {
			for j := 0; j < int(c.ChildCount()); j++ {
				cc := c.Child(j)
				if cc != nil && cc.Type() == "type_identifier" {
					name = cc.Content(src.Content)
					break
				}
			}
		}
	}

	if name == "" {
		return nil
	}

	// Extract leading comment (doc comment) if present.
	docComment := extractLeadingComment(node, lines)

	summary := fmt.Sprintf("Type: %s", name)
	if docComment != "" {
		summary = fmt.Sprintf("Type: %s — %s", name, truncate(docComment, 100))
	}

	content := map[string]any{
		"name":     name,
		"language": "go",
		"kind":     "type",
	}
	if docComment != "" {
		content["doc"] = docComment
	}

	return []Draft{{
		Summary: summary,
		Tags:    buildTags("go", "type", src.Path, name),
		Content: content,
		Provenance: ProvenanceMeta{
			SourceFile:   src.Path,
			SourceLines:  lineRange(startLine+1, endLine+1),
			SourceHash:   srcHash,
			TriggerEvent: TriggerEvent,
		},
	}}
}

// goFuncDraft produces a fact draft for a top-level Go function declaration.
func goFuncDraft(node *sitter.Node, src Source, srcHash string, lines []string) *Draft {
	startLine := int(node.StartPoint().Row)
	endLine := int(node.EndPoint().Row)

	name := ""
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c != nil && c.Type() == "identifier" {
			name = c.Content(src.Content)
			break
		}
	}
	if name == "" {
		return nil
	}

	// Skip unexported functions.
	if len(name) > 0 && name[0] >= 'a' && name[0] <= 'z' {
		return nil
	}

	docComment := extractLeadingComment(node, lines)
	summary := fmt.Sprintf("Function: %s", name)
	if docComment != "" {
		summary = fmt.Sprintf("Function: %s — %s", name, truncate(docComment, 100))
	}

	content := map[string]any{
		"name":     name,
		"language": "go",
		"kind":     "function",
	}
	if docComment != "" {
		content["doc"] = docComment
	}

	return &Draft{
		Summary: summary,
		Tags:    buildTags("go", "function", src.Path, name),
		Content: content,
		Provenance: ProvenanceMeta{
			SourceFile:   src.Path,
			SourceLines:  lineRange(startLine+1, endLine+1),
			SourceHash:   srcHash,
			TriggerEvent: TriggerEvent,
		},
	}
}

// goMethodDraft produces a fact draft for a top-level Go method declaration.
func goMethodDraft(node *sitter.Node, src Source, srcHash string, lines []string) *Draft {
	startLine := int(node.StartPoint().Row)
	endLine := int(node.EndPoint().Row)

	name := ""
	receiver := ""

	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c == nil {
			continue
		}
		if c.Type() == "field_identifier" {
			name = c.Content(src.Content)
		}
		if c.Type() == "parameter_list" && receiver == "" {
			// Receiver is the first parameter list.
			receiver = c.Content(src.Content)
		}
	}

	if name == "" {
		return nil
	}

	// Skip unexported methods.
	if len(name) > 0 && name[0] >= 'a' && name[0] <= 'z' {
		return nil
	}

	docComment := extractLeadingComment(node, lines)
	summary := fmt.Sprintf("Method: %s%s", receiver, name)
	if docComment != "" {
		summary = fmt.Sprintf("Method: %s%s — %s", receiver, name, truncate(docComment, 100))
	}

	content := map[string]any{
		"name":     name,
		"receiver": receiver,
		"language": "go",
		"kind":     "method",
	}
	if docComment != "" {
		content["doc"] = docComment
	}

	// Extract receiver type name for tagging.
	var extras []string
	if receiver != "" {
		recType := extractReceiverType(receiver)
		if recType != "" {
			extras = append(extras, "receiver-"+recType)
		}
	}

	return &Draft{
		Summary: summary,
		Tags:    buildTags("go", "method", src.Path, name, extras...),
		Content: content,
		Provenance: ProvenanceMeta{
			SourceFile:   src.Path,
			SourceLines:  lineRange(startLine+1, endLine+1),
			SourceHash:   srcHash,
			TriggerEvent: TriggerEvent,
		},
	}
}

// extractLeadingComment returns the text of a Go doc comment immediately
// preceding node, if any. It looks at the lines immediately above node's start.
func extractLeadingComment(node *sitter.Node, lines []string) string {
	startLine := int(node.StartPoint().Row)
	if startLine == 0 || len(lines) == 0 || startLine > len(lines) {
		return ""
	}

	var commentLines []string
	for i := startLine - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "//") {
			commentLines = append([]string{strings.TrimPrefix(strings.TrimPrefix(line, "//"), " ")}, commentLines...)
		} else {
			break
		}
	}

	return strings.TrimSpace(strings.Join(commentLines, " "))
}
