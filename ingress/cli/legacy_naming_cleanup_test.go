package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindMomDirDoesNotFallbackToLegacyLeoDirectory(t *testing.T) {
	dir := t.TempDir()
	legacyDir := filepath.Join(dir, ".leo")
	if err := os.MkdirAll(filepath.Join(legacyDir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "config.yaml"), []byte("version: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	_, err := findMomDir()
	if err == nil {
		t.Fatal("findMomDir should not resolve legacy .leo directories")
	}
	if strings.Contains(err.Error(), ".leo") {
		t.Fatalf("error should not expose legacy LEO path guidance: %v", err)
	}
}
