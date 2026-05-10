package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorBase_LegacyLayoutDetection verifies that when .mom/kb/ exists,
// doctor prints the migration prompt and exits cleanly without running normal checks.
func TestDoctorBase_LegacyLayoutDetection(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")

	// Create legacy layout: .mom/kb/ present (pre-v0.8.0 structure).
	os.MkdirAll(filepath.Join(momDir, "kb", "docs"), 0755)
	os.MkdirAll(filepath.Join(momDir, "kb", "constraints"), 0755)
	os.MkdirAll(filepath.Join(momDir, "kb", "skills"), 0755)

	// Write minimal config so findLeoDir can resolve the path.
	os.WriteFile(filepath.Join(momDir, "config.yaml"), []byte("version: \"1\"\nruntime: claude\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("doctor returned unexpected error for legacy layout: %v\noutput:\n%s", err, buf.String())
	}

	out := buf.String()

	// Must contain the legacy layout warning.
	if !strings.Contains(out, "Legacy layout detected") {
		t.Errorf("expected 'Legacy layout detected' in output, got:\n%s", out)
	}

	// Must contain the upgrade instruction.
	if !strings.Contains(out, "mom upgrade") {
		t.Errorf("expected 'mom upgrade' in output, got:\n%s", out)
	}

	// Normal checks must NOT run — memory/ check output should be absent.
	if strings.Contains(out, "memory/: exists") || strings.Contains(out, "memory/:") {
		t.Errorf("normal doctor checks should be skipped for legacy layout, got:\n%s", out)
	}
}

// TestDoctorBase_ModernLayout_RunsNormally verifies that a modern (flat) layout
// does NOT trigger the legacy detection — normal checks run as expected.
func TestDoctorBase_ModernLayout_RunsNormally(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")

	// Create flat v0.8.0 layout — no kb/ directory.
	for _, d := range []string{"memory", "constraints", "skills", "logs", "cache"} {
		os.MkdirAll(filepath.Join(momDir, d), 0755)
	}

	os.WriteFile(filepath.Join(momDir, "config.yaml"), []byte("version: \"1\"\nruntime: claude\n"), 0644)
	os.WriteFile(filepath.Join(momDir, "index.json"), []byte(`{"version":"1","by_tag":{},"by_type":{},"by_scope":{},"by_lifecycle":{}}`), 0644)
	os.WriteFile(filepath.Join(momDir, "schema.json"), []byte(`{"version":"1"}`), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	// May return an error (e.g. missing docs) but must NOT print legacy layout message.
	rootCmd.Execute()

	out := buf.String()

	if strings.Contains(out, "Legacy layout detected") {
		t.Errorf("modern layout must not trigger legacy detection, got:\n%s", out)
	}

	// Normal checks must have run — .mom/ directory check must appear.
	if !strings.Contains(out, ".mom/ directory") {
		t.Errorf("expected normal doctor checks to run for modern layout, got:\n%s", out)
	}
}
