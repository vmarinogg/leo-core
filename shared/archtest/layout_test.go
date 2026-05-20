package archtest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// allowedTopLevelDirs is the role-based layout from ADR 0017. Adding a
// new top-level bucket requires updating this list and the ADR.
var allowedTopLevelDirs = map[string]bool{
	"bus":      true,
	"cmd":      true,
	"docs":     true,
	"events":   true,
	"ingress":  true,
	"ops":      true,
	"services": true,
	"shared":   true,
	"storage":  true,
	"workers":  true,
}

// allowedTopLevelFiles are the repo-root files that ship in the module.
// Anything else at the root is flagged. Test entries omitted from
// gitignored paths.
var allowedTopLevelFiles = map[string]bool{
	".gitignore":         true,
	".golangci.yml":      true,
	".mcp.json":          true,
	".mcp.json.bkp":      true,
	".mom-project.yaml":  true,
	"CODE_OF_CONDUCT.md": true,
	"CONTRIBUTING.md":    true,
	"LICENSE":            true,
	"Makefile":           true,
	"README.md":          true,
	"SECURITY.md":        true,
	"go.mod":             true,
	"go.sum":             true,
}

// allowedTopLevelDirsAux are dirs that live at the root but aren't Go
// buckets — assets, formula taps, github metadata, etc. Includes
// gitignored local dev dirs (goals/, scripts/) so the test passes on
// developer machines as well as in CI.
var allowedTopLevelDirsAux = map[string]bool{
	".git":       true,
	".github":    true,
	".claude":    true,
	".worktrees": true,
	"adr":        true,
	"assets":     true,
	"Formula":    true,
	"prd":        true,
	"skills":     true,
	"goals":      true, // gitignored — local goal packages
	"scripts":    true, // gitignored — local helper scripts
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile is .../mom/shared/archtest/layout_test.go — three up.
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// TestLayout_TopLevelMatchesADR0017 asserts the repo root contains only
// the role-based buckets declared in ADR 0017 (plus a small allowlist
// of meta dirs/files). Adding a new top-level requires updating the
// allowlist and the ADR together.
func TestLayout_TopLevelMatchesADR0017(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", root, err)
	}

	var unexpected []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if !allowedTopLevelDirs[name] && !allowedTopLevelDirsAux[name] {
				unexpected = append(unexpected, "dir/"+name)
			}
			continue
		}
		if strings.HasPrefix(name, ".DS_Store") {
			continue
		}
		if !allowedTopLevelFiles[name] {
			unexpected = append(unexpected, "file/"+name)
		}
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Errorf("unexpected entries at repo root (update ADR 0017 + this test if intentional):\n  %s",
			strings.Join(unexpected, "\n  "))
	}
}

// TestLayout_NoCliInternalRegression asserts no package re-introduces
// the legacy cli/internal/ path. The reorg in #355 removed it; this
// test fails loud if a future PR brings it back.
func TestLayout_NoCliInternalRegression(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "cli", "internal")
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("cli/internal/ exists at %s — the v0.50 reorg (#355) removed it; do not re-introduce", dir)
	}
}

// TestLayout_NoCliDirAtAll asserts the entire cli/ directory is gone.
// ADR 0017 moved go.mod, go.sum, Makefile, and cmd/mom to the repo
// root; cli/ is a relic.
func TestLayout_NoCliDirAtAll(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "cli")
	if _, err := os.Stat(dir); err == nil {
		t.Fatalf("cli/ directory exists at %s — ADR 0017 moved its contents to the repo root", dir)
	}
}

// TestLayout_RequiredBucketsExist asserts every role-based bucket
// declared in ADR 0017 is present. Adding code without first naming
// its bucket is a process miss the layout should catch.
func TestLayout_RequiredBucketsExist(t *testing.T) {
	root := repoRoot(t)
	required := []string{
		"bus", "cmd", "events", "ingress",
		"ops", "services", "shared", "storage", "workers",
	}
	for _, bucket := range required {
		path := filepath.Join(root, bucket)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Errorf("required bucket missing: %s (ADR 0017)", bucket)
		}
	}
}
