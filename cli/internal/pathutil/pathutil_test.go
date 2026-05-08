package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalDirResolvesSymlinkedDirectory(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real", "project")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(base, "link-project")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	want := CanonicalDir(realDir)
	if got := CanonicalDir(linkDir); got != want {
		t.Fatalf("CanonicalDir(%q) = %q, want %q", linkDir, got, want)
	}
}

func TestCanonicalDirFallsBackForMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	want, err := filepath.Abs(missing)
	if err != nil {
		t.Fatal(err)
	}
	if got := CanonicalDir(missing); got != want {
		t.Fatalf("CanonicalDir(%q) = %q, want %q", missing, got, want)
	}
}
