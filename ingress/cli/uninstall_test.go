package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/ops/daemon"
)

// setupUninstallTestEnv isolates HOME and MOM_VAULT to temp dirs and changes
// cwd to a fresh tempdir. Returns (home, cwd, vaultPath).
func setupUninstallTestEnv(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, ".mom", "central.db"))
	vaultPath, err := canonical.Path()
	if err != nil {
		t.Fatalf("canonical.Path: %v", err)
	}

	cwd := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	return home, cwd, vaultPath
}

// runUninstallCmd invokes `mom uninstall` via rootCmd with the supplied stdin.
func runUninstallCmd(t *testing.T, stdin string) (string, error) {
	t.Helper()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs([]string{"uninstall"})
	err := rootCmd.Execute()
	return buf.String(), err
}

// Path 1 (disconnect this project): removes harness context files and the
// project's watch-registry entry, but leaves the central vault untouched.
func TestUninstall_DisconnectProject_RemovesHarnessFilesPreservesVault(t *testing.T) {
	_, _, vaultPath := setupUninstallTestEnv(t)

	// Pre-create central vault and assert its bytes survive.
	if err := os.MkdirAll(filepath.Dir(vaultPath), 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	if err := os.WriteFile(vaultPath, []byte("vault-bytes"), 0o644); err != nil {
		t.Fatalf("write vault: %v", err)
	}

	// Create a project root and chdir into it.
	projectRoot := t.TempDir()
	if err := os.Chdir(projectRoot); err != nil {
		t.Fatalf("chdir project: %v", err)
	}

	// Pre-create claude harness files inside the project.
	claudeMd := filepath.Join(projectRoot, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(claudeMd, []byte("generated"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	mcpJSON := filepath.Join(projectRoot, ".mcp.json")
	if err := os.WriteFile(mcpJSON, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write .mcp.json: %v", err)
	}

	// Register the project in the global watch registry.
	if err := daemon.RegisterProject(projectRoot, filepath.Join(projectRoot, ".mom"), []string{"claude"}); err != nil {
		t.Fatalf("register project: %v", err)
	}

	out, err := runUninstallCmd(t, "1\ny\n")
	if err != nil {
		t.Fatalf("disconnect returned error: %v\noutput:\n%s", err, out)
	}

	// Harness files removed.
	if _, err := os.Stat(claudeMd); err == nil {
		t.Errorf(".claude/CLAUDE.md should have been removed")
	}
	if _, err := os.Stat(mcpJSON); err == nil {
		t.Errorf(".mcp.json should have been removed")
	}

	// Central vault preserved.
	got, readErr := os.ReadFile(vaultPath)
	if readErr != nil {
		t.Fatalf("central vault must survive disconnect: %v", readErr)
	}
	if string(got) != "vault-bytes" {
		t.Errorf("central vault bytes mutated; got %q", string(got))
	}

	// Project unregistered.
	reg, regErr := daemon.LoadRegistry()
	if regErr != nil {
		t.Fatalf("load registry: %v", regErr)
	}
	for k := range reg {
		if k == projectRoot {
			t.Errorf("project still present in watch registry after disconnect")
		}
	}
}

// Full uninstall tears down the global watch daemon: clears all watch
// registry entries and removes any installed daemon service files.
func TestUninstall_FullUninstall_RemovesGlobalWatchDaemon(t *testing.T) {
	home, _, _ := setupUninstallTestEnv(t)

	// Register two projects in the global registry to prove the full
	// uninstall clears everything, not just cwd.
	p1 := t.TempDir()
	p2 := t.TempDir()
	if err := daemon.RegisterProject(p1, filepath.Join(p1, ".mom"), []string{"claude"}); err != nil {
		t.Fatalf("register p1: %v", err)
	}
	if err := daemon.RegisterProject(p2, filepath.Join(p2, ".mom"), []string{"claude"}); err != nil {
		t.Fatalf("register p2: %v", err)
	}

	// Pre-create the platform-specific daemon service file under the
	// isolated HOME so UninstallGlobal has something to remove. On macOS
	// this is the launchd plist; on Linux the systemd user unit.
	serviceFile, err := daemon.GlobalDaemonFile()
	if err != nil {
		t.Fatalf("resolving daemon service path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(serviceFile), 0o755); err != nil {
		t.Fatalf("mkdir daemon service dir: %v", err)
	}
	if err := os.WriteFile(serviceFile, []byte("placeholder\n"), 0o644); err != nil {
		t.Fatalf("write daemon service file: %v", err)
	}
	_ = home

	out, err := runUninstallCmd(t, "2\ndelete everything\n")
	if err != nil {
		t.Fatalf("full uninstall returned error: %v\noutput:\n%s", err, out)
	}

	// Watch registry must be empty after full uninstall.
	reg, regErr := daemon.LoadRegistry()
	if regErr != nil {
		t.Fatalf("load registry: %v", regErr)
	}
	if len(reg) != 0 {
		t.Errorf("watch registry must be empty after full uninstall, got %d entries", len(reg))
	}

	// Daemon service file must be removed by full uninstall, regardless
	// of platform.
	if _, err := os.Stat(serviceFile); err == nil {
		t.Errorf("daemon service file must be removed by full uninstall: %s", serviceFile)
	}
}

// Full uninstall strips the MOM block from ~/.claude/CLAUDE.md while
// preserving the user's surrounding content.
func TestUninstall_FullUninstall_StripsGlobalHarnessContext(t *testing.T) {
	home, _, _ := setupUninstallTestEnv(t)

	claudeMd := filepath.Join(home, ".claude", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	content := "# My personal notes\n\nKeep me.\n\n" +
		"<!-- BEGIN MOM GENERATED BLOCK -->\n" +
		"mom-block-body\n" +
		"<!-- END MOM GENERATED BLOCK -->\n\n" +
		"More personal notes.\n"
	if err := os.WriteFile(claudeMd, []byte(content), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	out, err := runUninstallCmd(t, "2\ndelete everything\n")
	if err != nil {
		t.Fatalf("full uninstall returned error: %v\noutput:\n%s", err, out)
	}

	got, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatalf("CLAUDE.md should still exist (only the MOM block is stripped): %v", err)
	}
	gotStr := string(got)
	if strings.Contains(gotStr, "BEGIN MOM GENERATED BLOCK") || strings.Contains(gotStr, "mom-block-body") {
		t.Errorf("MOM block must be removed from CLAUDE.md, got:\n%s", gotStr)
	}
	if !strings.Contains(gotStr, "My personal notes") || !strings.Contains(gotStr, "More personal notes.") {
		t.Errorf("user content must be preserved, got:\n%s", gotStr)
	}
}

// Path 2 + correct confirmation phrase deletes the central vault file.
func TestUninstall_FullUninstall_CorrectPhraseRemovesVault(t *testing.T) {
	_, _, vaultPath := setupUninstallTestEnv(t)

	if err := os.MkdirAll(filepath.Dir(vaultPath), 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	if err := os.WriteFile(vaultPath, []byte("vault-bytes"), 0o644); err != nil {
		t.Fatalf("write vault: %v", err)
	}

	out, err := runUninstallCmd(t, "2\ndelete everything\n")
	if err != nil {
		t.Fatalf("full uninstall returned error: %v\noutput:\n%s", err, out)
	}

	if _, err := os.Stat(vaultPath); err == nil {
		t.Errorf("central vault must be deleted after full uninstall with correct phrase")
	}
}

// Path 2 (full uninstall) requires typing the literal phrase "delete everything".
// Any other input must abort with no filesystem changes — guards the central vault.
func TestUninstall_FullUninstall_WrongPhraseAborts(t *testing.T) {
	_, cwd, vaultPath := setupUninstallTestEnv(t)

	// Pre-create a fake central vault file so we can verify it survives.
	if err := os.MkdirAll(filepath.Dir(vaultPath), 0o755); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	if err := os.WriteFile(vaultPath, []byte("vault-bytes"), 0o644); err != nil {
		t.Fatalf("write vault: %v", err)
	}

	out, err := runUninstallCmd(t, "2\nyes please\n")
	if err != nil {
		t.Fatalf("wrong-phrase abort returned error: %v\noutput:\n%s", err, out)
	}

	// Central vault must still exist with original bytes.
	got, readErr := os.ReadFile(vaultPath)
	if readErr != nil {
		t.Fatalf("central vault must survive wrong-phrase abort: %v", readErr)
	}
	if string(got) != "vault-bytes" {
		t.Errorf("central vault bytes mutated; got %q", string(got))
	}
	// cwd untouched.
	entries, _ := os.ReadDir(cwd)
	if len(entries) != 0 {
		t.Errorf("cwd should be untouched on abort, got %d entries", len(entries))
	}
	// Output must prove the path-2 warning was shown (so we know the
	// branch actually executed before the abort) AND that the run aborted.
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "delete everything") {
		t.Errorf("expected full-uninstall confirmation prompt mentioning 'delete everything', got:\n%s", out)
	}
	if !strings.Contains(lo, "cancel") && !strings.Contains(lo, "abort") {
		t.Errorf("expected cancel/abort message, got:\n%s", out)
	}
}

// Tracer bullet: choosing "0) Cancel" at the top menu exits cleanly and
// touches no filesystem state.
func TestUninstall_CancelAtTopMenu_NoFilesystemChanges(t *testing.T) {
	home, cwd, _ := setupUninstallTestEnv(t)

	out, err := runUninstallCmd(t, "0\n")
	if err != nil {
		t.Fatalf("cancel path returned error: %v\noutput:\n%s", err, out)
	}

	// Central vault directory must not have been created.
	if _, err := os.Stat(filepath.Join(home, ".mom")); err == nil {
		t.Errorf("HOME/.mom must not be created on cancel")
	}
	// Cwd must remain empty.
	entries, _ := os.ReadDir(cwd)
	if len(entries) != 0 {
		t.Errorf("cwd should remain empty on cancel, got %d entries", len(entries))
	}
	// Output should clearly say it was cancelled.
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "cancel") {
		t.Errorf("expected output to mention cancellation, got:\n%s", out)
	}
}
