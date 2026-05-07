package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/momhq/mom/cli/internal/centralvault"
)

func resetWatchFlagsForTest(t *testing.T) {
	t.Helper()
	oldTranscriptDir := watchTranscriptDir
	oldDebounceMs := watchDebounceMs
	oldStatus := watchStatus
	oldHarness := watchHarness
	oldSweep := watchSweep
	oldInstall := watchInstall
	oldUninstall := watchUninstall
	oldGlobal := watchGlobal
	t.Cleanup(func() {
		watchTranscriptDir = oldTranscriptDir
		watchDebounceMs = oldDebounceMs
		watchStatus = oldStatus
		watchHarness = oldHarness
		watchSweep = oldSweep
		watchInstall = oldInstall
		watchUninstall = oldUninstall
		watchGlobal = oldGlobal
	})
	watchTranscriptDir = ""
	watchDebounceMs = 300
	watchStatus = false
	watchHarness = "claude"
	watchSweep = false
	watchInstall = false
	watchUninstall = false
	watchGlobal = false
}

func TestWatchStatusUsesCentralVaultWithoutProjectMom(t *testing.T) {
	resetWatchFlagsForTest(t)
	t.Setenv("MOM_NO_DAEMON", "1")
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
	if got, err := centralvault.Dir(); err != nil || got != centralDir {
		t.Fatalf("central vault dir = %q, %v; want %q", got, err, centralDir)
	}

	origDir, _ := os.Getwd()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	watchStatus = true
	if err := runWatch(&cobra.Command{}, nil); err != nil {
		t.Fatalf("watch --status should use central vault without project .mom/: %v", err)
	}
}

func TestResolveMomContextFallsBackToCentralVault(t *testing.T) {
	projectDir := t.TempDir()
	centralDir := initTestCentralVault(t)
	if err := os.MkdirAll(centralDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(centralDir, "config.yaml"), []byte("version: \"1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	projectRoot, momDir, err := resolveMomContext(projectDir)
	if err != nil {
		t.Fatalf("resolveMomContext: %v", err)
	}
	if projectRoot != projectDir {
		t.Fatalf("projectRoot = %q, want %q", projectRoot, projectDir)
	}
	if momDir != centralDir {
		t.Fatalf("momDir = %q, want %q", momDir, centralDir)
	}
}
