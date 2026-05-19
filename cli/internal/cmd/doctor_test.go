package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/centralvault"
)

// setupDoctorCleanInstall provisions the minimum filesystem state that
// represents a healthy global MOM install. Returns the isolated home dir.
func setupDoctorCleanInstall(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, ".mom", "central.db"))

	// Central vault: a real openable DB with the v0.30 migrations applied.
	v, err := centralvault.Open()
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("close vault: %v", err)
	}

	// Daemon service file (macOS shape; doctor checks for either platform).
	plistDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(plistDir, 0o755); err != nil {
		t.Fatalf("mkdir LaunchAgents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plistDir, "com.momhq.watch.plist"), []byte("<plist/>"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}

	// Global harness MCP wiring.
	mcpJSON := `{"mcpServers":{"mom":{"type":"stdio","command":"mom","args":["serve","mcp"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatalf("write .claude.json: %v", err)
	}

	// Global harness context (MOM-managed block).
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	contextMd := "<!-- BEGIN MOM GENERATED BLOCK -->\nmom block body\n<!-- END MOM GENERATED BLOCK -->\n"
	if err := os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte(contextMd), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	return home
}

func runDoctorCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(append([]string{"doctor"}, args...))
	err := rootCmd.Execute()
	return buf.String(), err
}

// Vault DB missing → vault check fails with an actionable hint and the
// command exits with an error.
func TestDoctor_VaultMissing_FailsWithHint(t *testing.T) {
	setupDoctorCleanInstall(t)
	vaultPath, err := centralvault.Path()
	if err != nil {
		t.Fatalf("centralvault.Path: %v", err)
	}
	if err := os.Remove(vaultPath); err != nil {
		t.Fatalf("remove vault: %v", err)
	}

	out, err := runDoctorCmd(t)
	if err == nil {
		t.Fatalf("doctor must return error when vault missing, output:\n%s", out)
	}
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "central vault") {
		t.Errorf("expected output to mention central vault check, got:\n%s", out)
	}
	if !strings.Contains(lo, "mom init") {
		t.Errorf("expected next-action hint suggesting 'mom init', got:\n%s", out)
	}
}

// Vault file present but unopenable (corrupted bytes) → vault check
// fails with a different hint than the missing case.
func TestDoctor_VaultCorrupt_FailsWithDistinctHint(t *testing.T) {
	setupDoctorCleanInstall(t)
	vaultPath, err := centralvault.Path()
	if err != nil {
		t.Fatalf("centralvault.Path: %v", err)
	}
	// Overwrite with non-SQLite bytes so Open() fails.
	if err := os.WriteFile(vaultPath, []byte("not a sqlite db"), 0o644); err != nil {
		t.Fatalf("corrupt vault: %v", err)
	}

	out, err := runDoctorCmd(t)
	if err == nil {
		t.Fatalf("doctor must return error when vault is corrupt, output:\n%s", out)
	}
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "central vault") {
		t.Errorf("expected output to mention central vault check, got:\n%s", out)
	}
	// Distinct hint: should mention corruption / restore / re-init concept.
	// Must not be the same as the missing-file hint.
	if !strings.Contains(lo, "corrupt") && !strings.Contains(lo, "unopenable") && !strings.Contains(lo, "restore") {
		t.Errorf("expected corrupt-vault hint (corrupt/unopenable/restore), got:\n%s", out)
	}
}

// Daemon service file missing → watch-daemon check fails with hint.
func TestDoctor_DaemonServiceMissing_FailsWithHint(t *testing.T) {
	home := setupDoctorCleanInstall(t)
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.momhq.watch.plist")
	if err := os.Remove(plist); err != nil {
		t.Fatalf("remove plist: %v", err)
	}

	out, err := runDoctorCmd(t)
	if err == nil {
		t.Fatalf("doctor must return error when daemon missing, output:\n%s", out)
	}
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "watch daemon") {
		t.Errorf("expected output to mention watch daemon check, got:\n%s", out)
	}
	if !strings.Contains(lo, "mom init") {
		t.Errorf("expected next-action hint suggesting 'mom init', got:\n%s", out)
	}
}

// MCP wiring missing (mom entry absent from .claude.json) → fails with hint.
func TestDoctor_MCPWiringMissing_FailsWithHint(t *testing.T) {
	home := setupDoctorCleanInstall(t)
	// Overwrite with a .claude.json that has no mom entry.
	if err := os.WriteFile(filepath.Join(home, ".claude.json"),
		[]byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatalf("write claude.json: %v", err)
	}

	out, err := runDoctorCmd(t)
	if err == nil {
		t.Fatalf("doctor must return error when MCP entry missing, output:\n%s", out)
	}
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "harness mcp") {
		t.Errorf("expected output to mention harness mcp check, got:\n%s", out)
	}
	if !strings.Contains(lo, "mom init") {
		t.Errorf("expected next-action hint suggesting 'mom init', got:\n%s", out)
	}
}

// Global harness context missing MOM block → fails with hint.
func TestDoctor_HarnessContextMissing_FailsWithHint(t *testing.T) {
	home := setupDoctorCleanInstall(t)
	// Replace CLAUDE.md with content that has no MOM block.
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"),
		[]byte("# personal notes only\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	out, err := runDoctorCmd(t)
	if err == nil {
		t.Fatalf("doctor must return error when MOM block missing, output:\n%s", out)
	}
	lo := strings.ToLower(out)
	if !strings.Contains(lo, "harness context") {
		t.Errorf("expected output to mention harness context check, got:\n%s", out)
	}
	if !strings.Contains(lo, "mom init") {
		t.Errorf("expected next-action hint suggesting 'mom init', got:\n%s", out)
	}
}

// --bundle emits a deterministic blob with every check's status,
// suitable for pasting into a bug report. Running it twice in a row
// against the same state must yield byte-identical output (no
// timestamps, no PID, no PII).
func TestDoctor_Bundle_Deterministic(t *testing.T) {
	setupDoctorCleanInstall(t)

	first, err := runDoctorCmd(t, "--bundle")
	if err != nil {
		t.Fatalf("first --bundle run failed: %v\noutput:\n%s", err, first)
	}
	second, err := runDoctorCmd(t, "--bundle")
	if err != nil {
		t.Fatalf("second --bundle run failed: %v\noutput:\n%s", err, second)
	}
	if first != second {
		t.Errorf("--bundle output is non-deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// Bundle must include every check name.
	for _, name := range []string{
		"central vault",
		"watch daemon",
		"harness mcp",
		"harness context",
	} {
		if !strings.Contains(strings.ToLower(first), name) {
			t.Errorf("bundle missing check %q, got:\n%s", name, first)
		}
	}
}

// Doctor output (both human and bundle) must not reference any of the
// retired pre-central-vault commands. Regression guard per the #302
// design lock.
func TestDoctor_NoRetiredCommandReferences(t *testing.T) {
	setupDoctorCleanInstall(t)

	human, _ := runDoctorCmd(t)
	bundle, _ := runDoctorCmd(t, "--bundle")

	retired := []string{"reindex", "validate", "diagnose", "tour", "sweep"}
	for _, name := range retired {
		if strings.Contains(strings.ToLower(human), name) {
			t.Errorf("retired command %q referenced in human output:\n%s", name, human)
		}
		if strings.Contains(strings.ToLower(bundle), name) {
			t.Errorf("retired command %q referenced in bundle output:\n%s", name, bundle)
		}
	}
}

// Tracer bullet: a healthy global install passes every doctor check.
func TestDoctor_CleanInstall_AllChecksPass(t *testing.T) {
	setupDoctorCleanInstall(t)

	out, err := runDoctorCmd(t)
	if err != nil {
		t.Fatalf("doctor on clean install must return nil error, got: %v\noutput:\n%s", err, out)
	}

	// Output must mention each check name. Use simple substrings that
	// would survive cosmetic UX tweaks.
	for _, name := range []string{
		"central vault",
		"watch daemon",
		"harness mcp",
		"harness context",
	} {
		if !strings.Contains(strings.ToLower(out), name) {
			t.Errorf("expected output to mention %q check, got:\n%s", name, out)
		}
	}
}
