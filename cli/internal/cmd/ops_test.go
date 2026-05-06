package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/momhq/mom/cli/internal/config"
)

// setupTestMemoryWithConfig creates a .mom/ with config.yaml and returns the temp dir.
func setupTestMemoryWithConfig(t *testing.T, runtime string) string {
	t.Helper()
	dir := setupTestMemory(t) // reuse existing helper from memory_test.go (formerly kb_test.go)

	leoDir := filepath.Join(dir, ".mom")

	// Write a real config.yaml.
	cfg := config.Default()
	// Default() already includes claude; if a different runtime is requested,
	// add it (for test flexibility).
	if runtime != "claude" {
		cfg.Harnesses[runtime] = config.HarnessConfig{Enabled: true}
	}
	if err := config.Save(leoDir, &cfg); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	// Create profiles dir with a valid profile.
	profilesDir := filepath.Join(leoDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		t.Fatalf("creating profiles dir: %v", err)
	}
	profileData := []byte("name: Generalist\ndescription: test\n")
	os.WriteFile(filepath.Join(profilesDir, "generalist.yaml"), profileData, 0644)

	return dir
}

// ── mom status tests ─────────────────────────────────────────────────────────

func TestStatusCmd_ShowsCentralShape(t *testing.T) {
	lib := openCentralTestLib(t)
	insertCentralTestMemory(t, lib, "ops status memory", "status check")

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"MOM", "vault", "memories", "types", "landmarks", "op events", "constraints", "skills"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// ── leo doctor tests ──────────────────────────────────────────────────────────

func TestDoctorCmd_AllChecksPass(t *testing.T) {
	dir := setupTestMemoryWithConfig(t, "claude")
	writeTestDoc(t, dir, sampleDoc("doctor-doc"))

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor should pass, got error: %v\noutput:\n%s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "✔") {
		t.Errorf("expected checkmarks in output, got:\n%s", out)
	}
}

func TestDoctorCmd_MissingLeoDir(t *testing.T) {
	dir := t.TempDir() // no .mom/ at all

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error when .mom/ is missing")
	}
}

func TestDoctorCmd_InvalidConfigYaml(t *testing.T) {
	dir := setupTestMemory(t)
	leoDir := filepath.Join(dir, ".mom")

	// Write malformed YAML — {unclosed is guaranteed to fail yaml.Unmarshal.
	os.WriteFile(filepath.Join(leoDir, "config.yaml"), []byte("{unclosed\n"), 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for invalid config.yaml")
	}

	out := buf.String()
	if !strings.Contains(out, "✗") {
		t.Errorf("expected failure symbol in output, got:\n%s", out)
	}
}

func TestDoctorCmd_ShowsCheckSymbols(t *testing.T) {
	dir := setupTestMemoryWithConfig(t, "claude")

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	rootCmd.Execute()

	out := buf.String()
	// Most lines should have a check/cross/warning symbol.
	// Exceptions: blank lines, section headers (e.g. "Active scopes…:"),
	// and indented scope entries.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		// Section headers and indented detail lines are allowed without a symbol.
		if strings.HasPrefix(line, "Active scopes") ||
			strings.HasPrefix(line, "Adapter capabilities") ||
			strings.HasPrefix(line, "  ") {
			continue
		}
		hasSymbol := strings.Contains(line, "✔") ||
			strings.Contains(line, "✗") ||
			strings.Contains(line, "⚠")
		if !hasSymbol {
			t.Errorf("line missing check symbol: %q", line)
		}
	}
}

func TestDoctorCmd_ShowsScopesSection(t *testing.T) {
	dir := setupTestMemoryWithConfig(t, "claude")

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Set HOME to dir so scope.Walk finds the .mom/ there.
	t.Setenv("HOME", dir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	rootCmd.Execute()

	out := buf.String()
	if !strings.Contains(out, "Active scopes") {
		t.Errorf("expected 'Active scopes' section in doctor output, got:\n%s", out)
	}
	// The nearest scope should appear (repo label since no scope: in config from setupTestMemoryWithConfig).
	if !strings.Contains(out, "repo") {
		t.Errorf("expected 'repo' scope label in doctor output, got:\n%s", out)
	}
}

func TestDoctorCmd_InvalidDocFails(t *testing.T) {
	dir := setupTestMemoryWithConfig(t, "claude")
	leoDir := filepath.Join(dir, ".mom")

	// Write a corrupt JSON doc directly (bypassing adapter validation).
	corruptDoc := []byte(`{"id": "corrupt", "type": ""}`)
	os.WriteFile(filepath.Join(leoDir, "memory", "corrupt.json"), corruptDoc, 0644)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error for corrupt doc")
	}

	out := buf.String()
	if !strings.Contains(out, "✗") {
		t.Errorf("expected failure symbol in output for corrupt doc, got:\n%s", out)
	}
}

func TestDoctorCmd_OrphanIndexEntry(t *testing.T) {
	dir := setupTestMemoryWithConfig(t, "claude")
	leoDir := filepath.Join(dir, ".mom")

	// Write a doc, then remove it from disk (leaving index orphan).
	writeTestDoc(t, dir, sampleDoc("orphan-doc"))
	os.Remove(filepath.Join(leoDir, "memory", "orphan-doc.json"))

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor"})

	// Should fail or warn about orphan.
	rootCmd.Execute()

	out := buf.String()
	hasIssue := strings.Contains(out, "✗") || strings.Contains(out, "⚠")
	if !hasIssue {
		t.Errorf("expected warning or failure for orphan index entry, got:\n%s", out)
	}
}

// TestHelperYamlParsesBadYaml confirms that {unclosed fails yaml.Unmarshal.
func TestHelperYamlParsesBadYaml(t *testing.T) {
	var v map[string]any
	err := yaml.Unmarshal([]byte("{unclosed\n"), &v)
	if err == nil {
		t.Fatal("expected yaml.Unmarshal to fail for '{unclosed' input")
	}
}
