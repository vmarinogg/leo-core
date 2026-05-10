package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpgradeRejectsPreV030LeoLayout(t *testing.T) {
	dir := t.TempDir()
	leoDir := filepath.Join(dir, ".leo")
	if err := os.MkdirAll(leoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leoDir, "config.yaml"), []byte("version: \"1\"\nharnesses: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"upgrade"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("upgrade should reject pre-v0.30 .leo layout")
	}
	if !strings.Contains(err.Error(), "upgrade to MOM v0.30 first") {
		t.Fatalf("error = %v, want v0.30 hop guidance", err)
	}
}

func TestUpgradeRejectsPreV030KBLayout(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	if err := os.MkdirAll(filepath.Join(momDir, "kb", "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(momDir, "config.yaml"), []byte("version: \"1\"\nharnesses: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"upgrade"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("upgrade should reject pre-v0.30 kb layout")
	}
	if !strings.Contains(err.Error(), "upgrade to MOM v0.30 first") {
		t.Fatalf("error = %v, want v0.30 hop guidance", err)
	}
}

func TestCommandPackage_DoesNotKeepLegacyUpgradeImportScaffolding(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate cmd package directory")
	}
	cmdDir := filepath.Dir(thisFile)
	for _, name := range []string{"upgrade_import.go", "upgrade_logs_import.go"} {
		path := filepath.Join(cmdDir, name)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("legacy upgrade import scaffolding still exists: %s", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("checking %s: %v", name, err)
		}
	}
}
