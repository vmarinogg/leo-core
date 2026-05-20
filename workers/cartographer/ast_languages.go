package cartographer

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// languageHandler bundles a tree-sitter language with its query and file extensions.
type languageHandler struct {
	name       string
	language   *sitter.Language
	query      string
	extensions []string
}

// astLanguageRegistry is the ordered list of supported languages.
var astLanguageRegistry = []languageHandler{
	{
		name:       "go",
		language:   golang.GetLanguage(),
		extensions: []string{".go"},
		// Go uses legacy manual walking; query is not used.
	},
	{
		name:       "python",
		language:   python.GetLanguage(),
		extensions: []string{".py"},
		query: `
(class_definition name: (identifier) @name)
(function_definition name: (identifier) @name)
`,
	},
	{
		name:       "javascript",
		language:   javascript.GetLanguage(),
		extensions: []string{".js", ".mjs", ".cjs", ".jsx"},
		query: `
(class_declaration name: (identifier) @name)
(function_declaration name: (identifier) @name)
(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression)]))
(variable_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression)]))
`,
	},
	{
		name:       "typescript",
		language:   typescript.GetLanguage(),
		extensions: []string{".ts"},
		query: `
(class_declaration name: (type_identifier) @name)
(interface_declaration name: (type_identifier) @name)
(function_declaration name: (identifier) @name)
(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression)]))
`,
	},
	{
		name:       "tsx",
		language:   tsx.GetLanguage(),
		extensions: []string{".tsx"},
		query: `
(class_declaration name: (type_identifier) @name)
(function_declaration name: (identifier) @name)
(lexical_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression) (jsx_element) (jsx_self_closing_element)]))
`,
	},
	{
		name:       "rust",
		language:   rust.GetLanguage(),
		extensions: []string{".rs"},
		query: `
(struct_item name: (type_identifier) @name)
(enum_item name: (type_identifier) @name)
(trait_item name: (type_identifier) @name)
(impl_item type: (type_identifier) @name)
(function_item name: (identifier) @name)
`,
	},
	{
		name:       "java",
		language:   java.GetLanguage(),
		extensions: []string{".java"},
		query: `
(class_declaration name: (identifier) @name)
(interface_declaration name: (identifier) @name)
(enum_declaration name: (identifier) @name)
(method_declaration name: (identifier) @name)
`,
	},
	{
		name:       "ruby",
		language:   ruby.GetLanguage(),
		extensions: []string{".rb"},
		query: `
(class name: [(constant) (scope_resolution)] @name)
(module name: [(constant) (scope_resolution)] @name)
(method name: (identifier) @name)
(singleton_method name: (identifier) @name)
`,
	},
	{
		name:       "c",
		language:   c.GetLanguage(),
		extensions: []string{".c", ".h"},
		query: `
(struct_specifier name: (type_identifier) @name)
(type_definition
  declarator: (type_identifier) @name)
(function_definition
  declarator: (function_declarator
    declarator: (identifier) @name))
(function_definition
  declarator: (pointer_declarator
    declarator: (function_declarator
      declarator: (identifier) @name)))
`,
	},
	{
		name:       "cpp",
		language:   cpp.GetLanguage(),
		extensions: []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx", ".hh"},
		query: `
(struct_specifier name: (type_identifier) @name)
(class_specifier name: (type_identifier) @name)
(type_definition
  declarator: (type_identifier) @name)
(function_definition
  declarator: (function_declarator
    declarator: [(identifier) (qualified_identifier)] @name))
(function_definition
  declarator: (reference_declarator
    (function_declarator
      declarator: [(identifier) (qualified_identifier)] @name)))
`,
	},
	{
		name:       "csharp",
		language:   csharp.GetLanguage(),
		extensions: []string{".cs"},
		query: `
(class_declaration name: (identifier) @name)
(interface_declaration name: (identifier) @name)
(record_declaration name: (identifier) @name)
(enum_declaration name: (identifier) @name)
(method_declaration name: (identifier) @name)
`,
	},
}

// extensionToHandler builds a fast lookup from extension → handler.
func buildExtensionIndex(registry []languageHandler) map[string]*languageHandler {
	m := make(map[string]*languageHandler, 32)
	for i := range registry {
		h := &registry[i]
		for _, ext := range h.extensions {
			m[ext] = h
		}
	}
	return m
}
