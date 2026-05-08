package cmd

import (
	"bytes"
	"fmt"
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
	resetInitFlags()
	t.Cleanup(resetInitFlags)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, ".mom", "central.db"))
	oldRunner := runExternalCommand
	runExternalCommand = func(string, ...string) ([]byte, error) { return []byte("ok"), nil }
	t.Cleanup(func() { runExternalCommand = oldRunner })
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
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})

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
	}
	globalExpected := []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".claude.json"),
	}
	// Retired and formerly generated central docs must NOT exist.
	retired := []string{
		".mom/profiles/general-manager.yaml",
		".mom/profiles/backend-engineer.yaml",
		".mom/constraints/delegation-mandatory.json",
		".mom/constraints/anti-hallucination.json",
		".mom/constraints/escalation-triggers.json",
		".mom/skills/task-intake.json",
		".mom/skills/session-wrap-up.json",
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
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected graceful skip when MOM already exists, got error: %v", err)
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Errorf("expected skip message in output, got: %s", buf.String())
	}
}

func TestInitCmd_ReinitRepairsMissingGlobalFiles(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("initial init failed: %v", err)
	}

	home := filepath.Dir(centralDir)
	contextPath := filepath.Join(home, ".claude", "CLAUDE.md")
	mcpPath := filepath.Join(home, ".claude.json")
	if err := os.Remove(contextPath); err != nil {
		t.Fatalf("removing global context file: %v", err)
	}
	if err := os.Remove(mcpPath); err != nil {
		t.Fatalf("removing global MCP file: %v", err)
	}

	buf.Reset()
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("reinit failed: %v", err)
	}

	if _, err := os.Stat(contextPath); err != nil {
		t.Fatalf("global context file was not repaired: %v", err)
	}
	if _, err := os.Stat(mcpPath); err != nil {
		t.Fatalf("global MCP file was not repaired: %v", err)
	}
	if !strings.Contains(buf.String(), "configuration up to date") {
		t.Errorf("expected up-to-date reinit message, got: %s", buf.String())
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
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude", "--force"})

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

func TestInitCmd_HarnessesFlagConfiguresGlobalHarnesses(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude,codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	home := filepath.Dir(centralDir)
	for _, path := range []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing global harness output: %s", path)
		}
	}

	cfg, err := config.Load(centralDir)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	if got := cfg.EnabledHarnesses(); len(got) != 2 {
		t.Errorf("expected 2 enabled harnesses, got %d: %v", len(got), got)
	}
}

func TestInitCmd_HarnessesAllUsesDetectedInstalledHarnesses(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	t.Setenv("PATH", t.TempDir())
	if err := os.MkdirAll(filepath.Join(filepath.Dir(centralDir), ".claude"), 0755); err != nil {
		t.Fatalf("creating fake Claude install: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(centralDir), ".codex"), 0755); err != nil {
		t.Fatalf("creating fake Codex install: %v", err)
	}
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "all"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	cfg, err := config.Load(centralDir)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	enabled := strings.Join(cfg.EnabledHarnesses(), ",")
	if !strings.Contains(enabled, "claude") || !strings.Contains(enabled, "codex") {
		t.Fatalf("expected detected harnesses to include claude and codex, got %v", cfg.EnabledHarnesses())
	}
	if strings.Contains(enabled, "windsurf") || strings.Contains(enabled, "pi") {
		t.Fatalf("expected undetected harnesses to be excluded, got %v", cfg.EnabledHarnesses())
	}
}

func TestInitCmd_InstallsSkillsForSelectedHarnesses(t *testing.T) {
	dir := t.TempDir()
	_ = initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	var calls []string
	oldRunner := runExternalCommand
	runExternalCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("ok"), nil
	}
	t.Cleanup(func() { runExternalCommand = oldRunner })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude,codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	want := []string{
		"npx skills add momhq/mom -g -s * -a claude-code -y",
		"npx skills add momhq/mom -g -s * -a codex -y",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("skills install calls mismatch\nwant: %v\n got: %v", want, calls)
	}
}

func TestInitCmd_FinalTextMentionsStatusSkillAndCLI(t *testing.T) {
	dir := t.TempDir()
	_ = initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "/mom-status") || !strings.Contains(out, "mom status") {
		t.Fatalf("final output should mention /mom-status and mom status:\n%s", out)
	}
}

func TestInitCmd_SkillsInstallFailureIsSoft(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	oldRunner := runExternalCommand
	runExternalCommand = func(name string, args ...string) ([]byte, error) {
		return []byte("network unavailable"), fmt.Errorf("npx failed")
	}
	t.Cleanup(func() { runExternalCommand = oldRunner })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init should soft-fail skills install, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(centralDir, "config.yaml")); err != nil {
		t.Fatalf("core init did not complete: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"skills install", "mom init --force", "npx skills add momhq/mom -g -s '*' -a claude-code -y"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "network unavailable") {
		t.Fatalf("init output should hide noisy skills CLI output:\n%s", out)
	}
}

func TestInitCmd_RuntimesFlagIsNotRegistered(t *testing.T) {
	if f := initCmd.Flags().Lookup("runtimes"); f != nil {
		t.Fatalf("--runtimes should not be registered")
	}
}

func TestInitCmd_MultiHarness(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude,codex"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Both global harness outputs should exist.
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

	// Config should have both harnesses.
	cfg, err := config.Load(centralDir)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	enabled := cfg.EnabledHarnesses()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled harnesses, got %d: %v", len(enabled), enabled)
	}
}

// Experimental warnings are intentionally absent from init output — too noisy for onboarding.

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
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})

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
	rootCmd.SetArgs([]string{"init", "--harnesses", "codex"})

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

func TestInitCmd_DoesNotCreateGeneratedCentralDocs(t *testing.T) {
	dir := t.TempDir()
	centralDir := initTestCentralVault(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"init", "--harnesses", "claude"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	constraintsDir := filepath.Join(centralDir, "constraints")
	entries, err := os.ReadDir(constraintsDir)
	if err != nil {
		t.Fatalf("constraints dir should exist: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("central vault should not create generated constraint files, got %d", len(entries))
	}

	skillsDir := filepath.Join(centralDir, "skills")
	skillEntries, err := os.ReadDir(skillsDir)
	if err != nil {
		t.Fatalf("skills dir should exist: %v", err)
	}
	if len(skillEntries) != 0 {
		t.Errorf("central vault should not create generated skill files, got %d", len(skillEntries))
	}
	if _, err := os.Stat(filepath.Join(dir, ".mom")); err == nil {
		t.Error("project-local .mom/ should not be created")
	}
}
