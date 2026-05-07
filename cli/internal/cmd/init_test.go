package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/config"
)

func initTestCentralVault(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, ".mom", "central.db"))
	dir, err := centralvault.Dir()
	if err != nil {
		t.Fatalf("centralvault.Dir: %v", err)
	}
	return dir
}

func TestInitCmd_CreatesLeoStructure(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Verify central .mom/ structure and global agent files.
	home := filepath.Dir(centralDir)
	centralExpected := []string{
		"config.yaml",
		"identity.json",
		"schema.json",
		"logs",
		"central.db",
		"constraints/anti-hallucination.json",
		"skills/session-wrap-up.json",
	}
	globalExpected := []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".claude.json"),
	}
	// Retired files must NOT exist.
	retired := []string{
		".mom/profiles/general-manager.yaml",
		".mom/profiles/backend-engineer.yaml",
		".mom/constraints/delegation-mandatory.json",
		".mom/skills/task-intake.json",
		".mom/kb",
	}
	for _, path := range retired {
		full := filepath.Join(centralDir, strings.TrimPrefix(path, ".mom/"))
		if _, err := os.Stat(full); err == nil {
			t.Errorf("retired file should not exist: %s", path)
		}
	}

	for _, path := range centralExpected {
		full := filepath.Join(centralDir, path)
		if _, err := os.Stat(full); err != nil {
			t.Errorf("missing expected central file: %s", path)
		}
	}
	for _, path := range globalExpected {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing expected global file: %s", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".mom")); err == nil {
		t.Error("project-local .mom/ should not be created")
	}

	// Verify central directories.
	dirs := []string{"memory", "skills", "constraints", "logs", "cache"}
	for _, d := range dirs {
		full := filepath.Join(centralDir, d)
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("missing expected central dir: %s", d)
		} else if !info.IsDir() {
			t.Errorf("expected %s to be a directory", d)
		}
	}
}

func TestInitCmd_SkipsScaffoldIfAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.MkdirAll(centralDir, 0755)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected graceful skip when .mom/ already exists, got error: %v", err)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected skip message in output, got: %s", buf.String())
	}
}

func TestInitCmd_ForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.MkdirAll(centralDir, 0755)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "claude", "--force"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init --force failed: %v", err)
	}

	// Should have created the central structure despite existing .mom/.
	if _, err := os.Stat(filepath.Join(centralDir, "config.yaml")); err != nil {
		t.Error("config.yaml not created with --force")
	}
	if _, err := os.Stat(filepath.Join(dir, ".mom")); err == nil {
		t.Error("project-local .mom/ should not be created with --force")
	}
}

func TestInitCmd_MultiRuntime(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "claude,codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Both global runtime outputs should exist.
	home := filepath.Dir(centralDir)
	files := map[string]string{
		filepath.Join(home, ".claude", "CLAUDE.md"): "Claude",
		filepath.Join(home, ".codex", "AGENTS.md"):  "Codex",
	}

	for path, name := range files {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing %s output: %s", name, path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".mom")); err == nil {
		t.Error("project-local .mom/ should not be created")
	}

	// Config should have both runtimes.
	cfg, err := config.Load(centralDir)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	enabled := cfg.EnabledRuntimes()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled runtimes, got %d: %v", len(enabled), enabled)
	}
}

// Experimental warnings were removed from init output in v0.12 — too noisy for onboarding.

// TestInitCmd_DefaultDeliversMinimalContent verifies that init with default config
// generates minimal MCP-first boot content (not the legacy full content).
func TestInitCmd_DefaultDeliversMinimalContent(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(filepath.Dir(centralDir), ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}

	s := string(content)

	// Must contain the MCP-first directive.
	if !strings.Contains(s, "mom_status") {
		t.Error("CLAUDE.md must contain mom_status for MCP-first delivery")
	}

	// Must NOT contain the verbose legacy sections.
	legacy := []string{"## Voice", "## Constraints", "## Skills", "## During work"}
	for _, section := range legacy {
		if strings.Contains(s, section) {
			t.Errorf("CLAUDE.md must not contain legacy section %q with default (mcp) delivery", section)
		}
	}
}

func TestInitCmd_BackupExistingFile(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Create a user-owned AGENTS.md
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# My custom agents"), 0644)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Project-local user file should be untouched because init installs globally.
	content, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal("AGENTS.md should still exist")
	}
	if string(content) != "# My custom agents" {
		t.Error("project AGENTS.md was modified")
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md.bkp")); err == nil {
		t.Error("project AGENTS.md should not be backed up during global init")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(centralDir), ".codex", "AGENTS.md")); err != nil {
		t.Error("global Codex AGENTS.md not created")
	}
}

func TestInitCmd_DoesNotCreateProjectLocalMom(t *testing.T) {
	projectDir := t.TempDir()
	centralDir := initTestCentralVault(t)

	cmd := &cobra.Command{}
	cmd.SetOut(new(bytes.Buffer))
	result := OnboardingResult{
		Harnesses:  []string{"claude"},
		Language:   "en",
		Mode:       "concise",
		InstallDir: projectDir,
		ScopeLabel: "repo",
	}
	if err := runInitWithConfig(cmd, projectDir, false, result); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(projectDir, ".mom")); err == nil {
		t.Fatal("project-local .mom/ should not be created")
	}
	if _, err := os.Stat(filepath.Join(centralDir, "config.yaml")); err != nil {
		t.Fatalf("central config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(centralDir), ".claude", "CLAUDE.md")); err != nil {
		t.Fatalf("global agent context file missing: %v", err)
	}
}

func TestInitCmd_CreatesConstraintsInCentralVault(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--runtimes", "claude"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	constraintsDir := filepath.Join(centralDir, "constraints")
	entries, err := os.ReadDir(constraintsDir)
	if err != nil {
		t.Fatalf("constraints dir should exist: %v", err)
	}
	if len(entries) == 0 {
		t.Error("central vault should have constraint files")
	}

	skillsDir := filepath.Join(centralDir, "skills")
	skillEntries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("skills dir should exist: %v", err)
	}
	if len(skillEntries) == 0 {
		t.Error("central vault should have skill files")
	}
	if _, err := os.Stat(filepath.Join(dir, ".mom")); err == nil {
		t.Error("project-local .mom/ should not be created")
	}
}

// TestParentScopeHasDir_Unit tests the parentScopeHasDir helper directly.
func TestParentScopeHasDir_Unit(t *testing.T) {
	// Setup: org/.mom/constraints/ with a file, repo under org.
	orgDir := t.TempDir()
	constraintsDir := filepath.Join(orgDir, ".mom", "constraints")
	os.MkdirAll(constraintsDir, 0755)
	os.WriteFile(filepath.Join(constraintsDir, "test.json"), []byte(`{}`), 0644)

	repoDir := filepath.Join(orgDir, "repo-a")
	os.MkdirAll(repoDir, 0755)

	// From repo dir, parent should have constraints.
	if !parentScopeHasDir(repoDir, "constraints") {
		t.Error("expected parentScopeHasDir to find constraints in parent")
	}

	// From repo dir, parent should NOT have skills (none created).
	if parentScopeHasDir(repoDir, "skills") {
		t.Error("expected parentScopeHasDir to not find skills in parent")
	}

	// From org dir itself, no parent has constraints.
	if parentScopeHasDir(orgDir, "constraints") {
		t.Error("expected parentScopeHasDir to not find constraints above org")
	}
}
