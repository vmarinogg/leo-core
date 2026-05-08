package archtest_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/momhq/mom/cli/internal/archtest"
)

// fixtureDir writes a tiny package with a known import set into a
// fresh temp dir and returns the dir path.
func fixtureDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestDirectImports_ReportsAllProductionFileImports(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"a.go": `package fx; import "fmt"; var _ = fmt.Sprint`,
		"b.go": `package fx; import "errors"; var _ = errors.New`,
	})

	got := archtest.DirectImports(t, dir)
	if !slices.Contains(got, "fmt") || !slices.Contains(got, "errors") {
		t.Errorf("got %v, want fmt and errors", got)
	}
}

// TestDirectImports_ExcludesTestFiles is the contract that lets us
// import "testing" from _test.go files without tripping arch rules
// against production sources.
func TestDirectImports_ExcludesTestFiles(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"a.go":      `package fx; import "fmt"; var _ = fmt.Sprint`,
		"a_test.go": `package fx; import "testing"; func TestX(t *testing.T) {}`,
	})

	got := archtest.DirectImports(t, dir)
	if slices.Contains(got, "testing") {
		t.Errorf("testing should be excluded (it lives only in _test.go); got %v", got)
	}
	if !slices.Contains(got, "fmt") {
		t.Errorf("fmt should be present (production source); got %v", got)
	}
}

// TestDirectImports_DeduplicatesAcrossFiles ensures the same import
// in two files surfaces once.
func TestDirectImports_DeduplicatesAcrossFiles(t *testing.T) {
	dir := fixtureDir(t, map[string]string{
		"a.go": `package fx; import "fmt"; var _ = fmt.Sprint`,
		"b.go": `package fx; import "fmt"; var _ = fmt.Println`,
	})

	got := archtest.DirectImports(t, dir)
	count := 0
	for _, p := range got {
		if p == "fmt" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("fmt appeared %d times, want 1 (dedupe failed)", count)
	}
}
