package storage_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoragePackage_DoesNotKeepLegacyMultiScopeReader(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate storage package directory")
	}
	pkgDir := filepath.Dir(thisFile)

	path := filepath.Join(pkgDir, "multiscope.go")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("legacy multi-scope reader still exists: multiscope.go")
	} else if !os.IsNotExist(err) {
		t.Fatalf("checking multiscope.go: %v", err)
	}
}
