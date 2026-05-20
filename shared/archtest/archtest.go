// Package archtest provides shared helpers for in-process
// architectural rule tests. It avoids the `exec.Command("go", "list",
// ...)` pattern so tests don't depend on the go toolchain being on
// $PATH at test time.
//
// The helpers parse a package's .go files (production sources only —
// _test.go files are excluded so test imports don't trip arch rules)
// and return the import set declared by `import (...)` clauses.
//
// Only direct imports are reported. The v0.30 architecture rules are
// shaped as direct-import constraints: "X must not import Y," where
// "import" means the textual import declaration. Transitive deps are
// not in scope — callers wanting that should iterate.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// DirectImports returns the union of all imports declared by .go files
// in pkgDir, excluding _test.go files. Paths are unquoted ("foo/bar",
// not `"foo/bar"`) and deduplicated.
//
// pkgDir is a filesystem path (typically obtained via a relative
// path like "../herald" from the test file's location, or from
// os.Getwd() + "/.." + sibling).
func DirectImports(t *testing.T, pkgDir string) []string {
	t.Helper()

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("archtest: read %s: %v", pkgDir, err)
	}

	seen := map[string]struct{}{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("archtest: parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("archtest: unquote %s in %s: %v", imp.Path.Value, path, err)
			}
			seen[p] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	return out
}

// AssertNoDirectImport fails t if pkgDir's direct imports contain any
// of forbidden. It is the canonical shape for v0.30 architectural
// rules: "package X must not directly import Y."
//
// Use one call per arch rule per package. If the same package has
// multiple distinct rules, multiple calls keep failure messages
// targeted.
func AssertNoDirectImport(t *testing.T, pkgDir string, forbidden ...string) {
	t.Helper()
	imports := DirectImports(t, pkgDir)
	bad := map[string]struct{}{}
	for _, f := range forbidden {
		bad[f] = struct{}{}
	}
	for _, imp := range imports {
		if _, ok := bad[imp]; ok {
			t.Errorf("forbidden direct import %q in %s", imp, pkgDir)
		}
	}
}
