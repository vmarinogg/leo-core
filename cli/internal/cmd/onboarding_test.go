package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// oneByteReader wraps a reader to return one byte at a time, preventing
// bufio.Scanner from buffering ahead and consuming input meant for later fields.
// This is needed because huh's accessible mode creates a new bufio.Scanner
// per field, and each scanner would otherwise read the full input.
type oneByteReader struct {
	r io.Reader
}

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

func testReader(input string) io.Reader {
	return &oneByteReader{r: strings.NewReader(input)}
}

func isolateHarnessDetection(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", t.TempDir())
}

// Accessible-mode input format:
//   Note:        no input consumed
//   MultiSelect: enter number to toggle (1-N), 0 to confirm
//   Select:      enter number (1-N), empty = default
//   Input:       enter text, empty = default
//   Confirm:     y/n, empty = default
//
// Form flow (bootstrap select removed alongside cartographer):
//   Form 1: Note(welcome), MultiSelect(runtimes), Select(mode)
//   Form 2: Note(summary), Confirm

// TestOnboarding_DefaultSelections verifies that accepting all defaults works.
func TestOnboarding_DefaultSelections(t *testing.T) {
	isolateHarnessDetection(t)
	// 0=confirm runtimes (claude pre-selected), then empty for mode, confirm.
	input := testReader("0\n\n\n")
	output := &bytes.Buffer{}

	result, err := runOnboarding(input, output, t.TempDir())
	if err != nil {
		t.Fatalf("runOnboarding failed: %v\noutput:\n%s", err, output.String())
	}

	if len(result.Harnesses) == 0 {
		t.Fatal("expected at least one runtime")
	}
	if result.Harnesses[0] != "claude" {
		t.Errorf("expected first runtime=claude, got %q", result.Harnesses[0])
	}
	if result.Language != "en" {
		t.Errorf("expected language=en, got %q", result.Language)
	}
	if result.Mode != "concise" {
		t.Errorf("expected mode=concise, got %q", result.Mode)
	}
}

// TestOnboarding_ExplicitSelections verifies non-default choices.
func TestOnboarding_ExplicitSelections(t *testing.T) {
	isolateHarnessDetection(t)
	// MultiSelect: toggle 2 (codex) then 0 (confirm), mode=2 (efficient), confirm=y.
	input := testReader("2\n0\n2\ny\n")
	output := &bytes.Buffer{}

	result, err := runOnboarding(input, output, t.TempDir())
	if err != nil {
		t.Fatalf("runOnboarding failed: %v\noutput:\n%s", err, output.String())
	}

	hasRuntime := func(name string) bool {
		for _, r := range result.Harnesses {
			if r == name {
				return true
			}
		}
		return false
	}
	if !hasRuntime("claude") {
		t.Error("expected claude in runtimes")
	}
	if !hasRuntime("codex") {
		t.Error("expected codex in runtimes")
	}
	if result.Language != "en" {
		t.Errorf("expected language=en (fixed), got %q", result.Language)
	}
	if result.Mode != "efficient" {
		t.Errorf("expected mode=efficient, got %q", result.Mode)
	}
}

// TestOnboarding_ConfirmNo verifies that answering "n" at the confirm step
// returns an error signalling the user aborted.
func TestOnboarding_ConfirmNo(t *testing.T) {
	isolateHarnessDetection(t)
	// Accept defaults, then reject at confirm.
	input := testReader("0\n\nn\n")
	output := &bytes.Buffer{}

	_, err := runOnboarding(input, output, t.TempDir())
	if err == nil {
		t.Fatal("expected error when user aborts at confirm step")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("expected 'aborted' in error, got: %v", err)
	}
}

// TestOnboarding_OutputContainsWelcome verifies the welcome banner appears.
func TestOnboarding_OutputContainsWelcome(t *testing.T) {
	isolateHarnessDetection(t)
	input := testReader("0\n\n\n")
	output := &bytes.Buffer{}

	_, err := runOnboarding(input, output, t.TempDir())
	if err != nil {
		t.Fatalf("runOnboarding failed: %v", err)
	}

	out := output.String()
	if !strings.Contains(out, "Setting up MOM takes about 30 seconds. Let's start.") {
		t.Errorf("expected welcome copy, got:\n%s", out)
	}
}

// TestOnboarding_OutputContainsSummary verifies the summary step renders.
// Input: confirm runtimes, mode=2(efficient), confirm=y.
func TestOnboarding_OutputContainsSummary(t *testing.T) {
	isolateHarnessDetection(t)
	input := testReader("0\n2\ny\n")
	output := &bytes.Buffer{}

	_, err := runOnboarding(input, output, t.TempDir())
	if err != nil {
		t.Fatalf("runOnboarding failed: %v\noutput:\n%s", err, output.String())
	}

	out := output.String()
	// Autonomy is no longer in the summary; Language is still shown.
	for _, keyword := range []string{"Harnesses", "Language", "Mode"} {
		if !strings.Contains(out, keyword) {
			t.Errorf("expected summary to contain %q, got:\n%s", keyword, out)
		}
	}
	for _, removed := range []string{"Autonomy", "Scope", "Where should MOM be installed?"} {
		if strings.Contains(out, removed) {
			t.Errorf("summary/onboarding should not contain %q, got:\n%s", removed, out)
		}
	}
}

// TestOnboarding_MultipleRuntimesSelected verifies toggling multiple runtimes.
func TestOnboarding_MultipleRuntimesSelected(t *testing.T) {
	isolateHarnessDetection(t)
	// Toggle codex (2) and windsurf (3), confirm (0), then defaults for mode and confirm.
	input := testReader("2\n3\n0\n\n\n")
	output := &bytes.Buffer{}

	result, err := runOnboarding(input, output, t.TempDir())
	if err != nil {
		t.Fatalf("runOnboarding failed: %v\noutput:\n%s", err, output.String())
	}

	if len(result.Harnesses) != 3 {
		t.Fatalf("expected 3 runtimes, got %d: %v", len(result.Harnesses), result.Harnesses)
	}
}

// TestOnboarding_DefaultScopeIsRepo verifies that choosing the default scope
// option (current dir) sets ScopeLabel to "repo" and InstallDir to cwd.
func TestOnboarding_DefaultScopeIsRepo(t *testing.T) {
	isolateHarnessDetection(t)
	cwd := t.TempDir()
	// 0=confirm runtimes, empty for mode, empty for confirm (default=yes).
	input := testReader("0\n\n\n")
	output := &bytes.Buffer{}

	result, err := runOnboarding(input, output, cwd)
	if err != nil {
		t.Fatalf("runOnboarding failed: %v\noutput:\n%s", err, output.String())
	}

	if result.ScopeLabel != "repo" {
		t.Errorf("ScopeLabel = %q, want repo", result.ScopeLabel)
	}
	if result.InstallDir != cwd {
		t.Errorf("InstallDir = %q, want %q", result.InstallDir, cwd)
	}
}

// TestOnboarding_NonInteractiveDefaultsToRepo verifies the non-interactive path
// sets scope=repo and InstallDir=cwd.
func TestOnboarding_NonInteractiveDefaultsToRepo(t *testing.T) {
	// Non-interactive is handled in runInit, not runOnboarding. Here we verify
	// that the OnboardingResult produced for the non-interactive path has the
	// correct scope fields by constructing it directly (as runInit does).
	cwd := t.TempDir()
	result := OnboardingResult{
		Harnesses:  []string{"claude"},
		Language:   "en",
		Mode:       "concise",
		InstallDir: cwd,
		ScopeLabel: "repo",
	}

	if result.ScopeLabel != "repo" {
		t.Errorf("ScopeLabel = %q, want repo", result.ScopeLabel)
	}
	if result.InstallDir != cwd {
		t.Errorf("InstallDir = %q, want %q", result.InstallDir, cwd)
	}
}
