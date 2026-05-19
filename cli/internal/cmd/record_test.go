package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
)

// resetRecordFlags must run before each test that drives runRecord —
// global flag vars persist between cobra invocations otherwise.
func resetRecordFlags() {
	recordSession = ""
	recordSummary = ""
	recordTags = nil
	recordActor = "cli"
}

// runRecordWithStdin replaces os.Stdin with a strings.Reader for the
// duration of the call. Returns whatever runRecord returned plus the
// stdout/stderr captured via a buffer.
func runRecordWithStdin(t *testing.T, stdin string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString(stdin); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev })

	buf := &bytes.Buffer{}
	recordCmd.SetOut(buf)
	recordCmd.SetErr(buf)
	err = runRecord(recordCmd, nil)
	return buf.String(), err
}

// openCentralVaultForTest opens the central librarian against an
// isolated $HOME/.mom/mom.db. Returns the librarian for read-back
// assertions.
func openCentralVaultForTest(t *testing.T) *librarian.Librarian {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("centralvault.OpenLibrarian: %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })
	return lib
}

// TestRunRecord_PersistsToCentralVault locks the human-path contract:
// stdin text + --session + tags lands as one memory in the central
// vault with manual-draft / record provenance and every tag linked.
func TestRunRecord_PersistsToCentralVault(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	recordSession = "11111111-1111-4111-8111-111111111111"
	recordSummary = "decision summary"
	recordTags = []string{"deploy", "decision"}
	recordActor = "vmarino"

	out, err := runRecordWithStdin(t, "decided to use Postgres for the canary")
	if err != nil {
		t.Fatalf("runRecord: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "recorded:") {
		t.Errorf("expected 'recorded:' in stdout, got %q", out)
	}

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "11111111-1111-4111-8111-111111111111", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1", len(rows))
	}
	m := rows[0].Memory
	if !strings.Contains(m.Content, "decided to use Postgres") {
		t.Errorf("Content lost original text: %q", m.Content)
	}
	if m.Summary != "decision summary" {
		t.Errorf("Summary = %q, want decision summary", m.Summary)
	}
	if m.ProvenanceTriggerEvent != "record" {
		t.Errorf("trigger = %q, want record", m.ProvenanceTriggerEvent)
	}
	if m.ProvenanceSourceType != "manual-draft" {
		t.Errorf("source_type = %q, want manual-draft", m.ProvenanceSourceType)
	}
	if m.ProvenanceActor != "vmarino" {
		t.Errorf("actor = %q, want vmarino (from --actor)", m.ProvenanceActor)
	}

	// Both tags linked atomically.
	for _, tag := range []string{"deploy", "decision"} {
		ids, err := lib.MemoriesByTag(tag)
		if err != nil {
			t.Fatalf("MemoriesByTag(%q): %v", tag, err)
		}
		if len(ids) != 1 || ids[0] != m.ID {
			t.Errorf("MemoriesByTag(%q) = %v, want [%q]", tag, ids, m.ID)
		}
	}
}

// TestRunRecord_JSONInput_SilentBail locks the hook-friendly contract:
// JSON-shaped stdin (legacy hook payload) returns nil, exits 0, and
// writes nothing. Old Claude/Codex hook configs that fire `mom record`
// must not pollute the vault with JSON-as-memory-text.
func TestRunRecord_JSONInput_SilentBail(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	recordSession = "11111111-1111-4111-8111-111111111111"
	out, err := runRecordWithStdin(t, `{"session_id":"abc","transcript_path":"/tmp/x"}`)
	if err != nil {
		t.Fatalf("runRecord should not error on JSON bail-out: %v", err)
	}
	if strings.Contains(out, "recorded:") {
		t.Errorf("unexpected write on JSON input: %q", out)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "11111111-1111-4111-8111-111111111111", Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories, want 0 (JSON bail-out must not persist)", len(rows))
	}
}

func TestRunRecord_UsesHarnessEnvSession(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "s-env")

	out, err := runRecordWithStdin(t, "some text with env session")
	if err != nil {
		t.Fatalf("runRecord: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "session=s-env") {
		t.Fatalf("output = %q, want session=s-env", out)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s-env", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1", len(rows))
	}
}

func TestRecordCmd_DoesNotSilenceHumanPathErrors(t *testing.T) {
	if recordCmd.SilenceErrors {
		t.Fatal("mom record must print explicit-record rejections to stderr")
	}
}

func TestRunRecord_MissingSessionRejectsRealText(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	out, err := runRecordWithStdin(t, "some text without a session flag")
	if err == nil {
		t.Fatalf("expected missing session error; output: %q", out)
	}
	if !strings.Contains(err.Error(), "session_id") {
		t.Fatalf("error = %v, want session_id", err)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories, want 0 (missing session must not persist)", len(rows))
	}
}

// TestRunRecord_EmptyStdin_SilentBail covers the third hook-friendly
// shape — empty stdin must not write anything regardless of flags.
func TestRunRecord_FakeSessionRejectsRealText(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	recordSession = "hidden-record-fake"
	out, err := runRecordWithStdin(t, "some text with fake session")
	if err == nil {
		t.Fatalf("expected fake session error; output: %q", out)
	}
	if !strings.Contains(err.Error(), "do not invent") {
		t.Fatalf("error = %v, want do not invent", err)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories, want 0 (fake session must not persist)", len(rows))
	}
}

func TestRunRecord_EmptyStdin_SilentBail(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	recordSession = "11111111-1111-4111-8111-111111111111"
	out, err := runRecordWithStdin(t, "")
	if err != nil {
		t.Fatalf("runRecord should not error on empty stdin: %v", err)
	}
	if strings.Contains(out, "recorded:") {
		t.Errorf("unexpected write on empty stdin: %q", out)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "11111111-1111-4111-8111-111111111111", Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories, want 0 (empty stdin must not persist)", len(rows))
	}
}

// TestRunRecord_RejectsEmptyNormalisedTag locks the parity with the
// MCP mom_record handler: any tag normalising to empty rejects the
// whole request rather than persisting a partial-tag memory. This
// path is on the human side — it returns a real error, not a silent
// bail.
func TestRunRecord_RejectsEmptyNormalisedTag(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	recordSession = "11111111-1111-4111-8111-111111111111"
	recordTags = []string{"valid", "!!!"} // second normalises to empty
	out, err := runRecordWithStdin(t, "some real memory text")
	if err == nil {
		t.Fatalf("expected error for empty-normalised tag; output: %q", out)
	}
	if !strings.Contains(err.Error(), "normalises to empty") {
		t.Errorf("error = %v, want 'normalises to empty'", err)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "11111111-1111-4111-8111-111111111111", Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories, want 0 (rejection must not persist)", len(rows))
	}
}

// TestRunRecord_StampsProjectIdFromBindFile locks ADR 0016 wiring on
// the CLI explicit-record path: when cwd has a .mom-project.yaml, the
// persisted memory carries the declared id.
func TestRunRecord_StampsProjectIdFromBindFile(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	// Bind cwd to project "alpha".
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, ".mom-project.yaml"),
		[]byte("version: \"1\"\nid: alpha\n"), 0o644); err != nil {
		t.Fatalf("write bind file: %v", err)
	}
	origDir, _ := os.Getwd()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	recordSession = "11111111-1111-4111-8111-111111111111"
	if _, err := runRecordWithStdin(t, "bound project capture"); err != nil {
		t.Fatalf("runRecord: %v", err)
	}

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "11111111-1111-4111-8111-111111111111", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1", len(rows))
	}
	if rows[0].Memory.ProjectId != "alpha" {
		t.Errorf("ProjectId = %q, want alpha", rows[0].Memory.ProjectId)
	}
}

// TestRunRecord_NullProjectIdWhenCwdUnbound: capture from an unbound cwd
// persists with empty ProjectId (ADR 0016 default).
func TestRunRecord_NullProjectIdWhenCwdUnbound(t *testing.T) {
	resetRecordFlags()
	lib := openCentralVaultForTest(t)

	projectDir := t.TempDir() // no .mom-project.yaml
	origDir, _ := os.Getwd()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	recordSession = "22222222-2222-4222-8222-222222222222"
	if _, err := runRecordWithStdin(t, "unbound capture"); err != nil {
		t.Fatalf("runRecord: %v", err)
	}

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "22222222-2222-4222-8222-222222222222", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1", len(rows))
	}
	if rows[0].Memory.ProjectId != "" {
		t.Errorf("ProjectId = %q, want empty (cwd unbound)", rows[0].Memory.ProjectId)
	}
}
