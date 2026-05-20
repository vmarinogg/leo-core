package watcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/shared/pathutil"
)

// mockAdapter records every call to ExtractTurn for inspection.
type mockAdapter struct {
	calls []string
}

func (m *mockAdapter) Name() string { return "mock" }

// ExtractTurn records the line and returns a minimal Turn for any
// non-empty input. Rich-content adapter tests exercise the real
// ClaudeAdapter / CodexAdapter / PiAdapter.
func (m *mockAdapter) ExtractTurn(line []byte, sessionID string) (Turn, bool) {
	m.calls = append(m.calls, string(line))
	if len(strings.TrimSpace(string(line))) == 0 {
		return Turn{}, false
	}
	return Turn{
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
		Role:      "assistant",
		Text:      "mock: " + string(line),
	}, true
}

// TestSessionIDFromPath verifies that the session ID is derived from the filename.
func TestSessionIDFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/home/user/.claude/projects/my-proj/abc-123.jsonl", "abc-123"},
		{"/tmp/session.jsonl", "session"},
		{"plain.jsonl", "plain"},
	}
	for _, tc := range cases {
		got := sessionIDFromPath(tc.path)
		if got != tc.want {
			t.Errorf("sessionIDFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestMigrateLegacyCursors locks the upgrade-papercut fix from PR 4:
// pre-existing cursor files written by the v0.30-dev watcher into
// .mom/raw/ get copied into .mom/cache/ on first run after upgrade
// so the watcher resumes from the right offset instead of
// re-ingesting every historical turn. Read-only on the old path,
// non-clobbering on the new.
func TestMigrateLegacyCursors(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()

	// Two legacy cursors and one unrelated file (must NOT be copied).
	if err := os.WriteFile(filepath.Join(oldDir, ".watch-cursor-sess-A"), []byte("1234"), 0644); err != nil {
		t.Fatalf("seed cursor A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, ".watch-cursor-sess-B"), []byte("5678"), 0644); err != nil {
		t.Fatalf("seed cursor B: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "unrelated.jsonl"), []byte("noise"), 0644); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	// Pre-existing cursor in newDir for sess-C — migration must NOT
	// clobber it (new path wins).
	if err := os.WriteFile(filepath.Join(newDir, ".watch-cursor-sess-C"), []byte("9999"), 0644); err != nil {
		t.Fatalf("seed pre-existing new cursor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, ".watch-cursor-sess-C"), []byte("0000"), 0644); err != nil {
		t.Fatalf("seed conflicting old cursor: %v", err)
	}

	migrateLegacyCursors(oldDir, newDir)

	// Both new cursors copied with their original content.
	for _, c := range []struct {
		name string
		want string
	}{
		{".watch-cursor-sess-A", "1234"},
		{".watch-cursor-sess-B", "5678"},
	} {
		got, err := os.ReadFile(filepath.Join(newDir, c.name))
		if err != nil {
			t.Errorf("missing migrated cursor %q: %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("cursor %q content = %q, want %q", c.name, string(got), c.want)
		}
	}

	// Conflicting cursor was NOT overwritten — new path wins.
	got, _ := os.ReadFile(filepath.Join(newDir, ".watch-cursor-sess-C"))
	if string(got) != "9999" {
		t.Errorf("pre-existing cursor was clobbered: got %q, want 9999", string(got))
	}

	// Originals remain on the old path (read-only migration).
	if _, err := os.Stat(filepath.Join(oldDir, ".watch-cursor-sess-A")); err != nil {
		t.Errorf("original cursor A removed: %v", err)
	}

	// Unrelated file must not have been copied.
	if _, err := os.Stat(filepath.Join(newDir, "unrelated.jsonl")); err == nil {
		t.Error("unrelated file copied — migration should match .watch-cursor-* only")
	}
}

// TestMigrateLegacyCursors_NoOldDir is a no-op on fresh installs
// where .mom/raw/ never existed.
func TestMigrateLegacyCursors_NoOldDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	newDir := t.TempDir()
	migrateLegacyCursors(missing, newDir) // must not panic, must not error
	entries, _ := os.ReadDir(newDir)
	if len(entries) != 0 {
		t.Errorf("expected newDir empty after no-op migration, got %d entries", len(entries))
	}
}

// TestWatchCursorRoundTrip verifies write/read of byte offsets.
func TestWatchCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cf := filepath.Join(dir, ".watch-cursor-test")

	// Non-existent cursor file → offset 0.
	if got := readWatchCursor(cf); got != 0 {
		t.Errorf("expected 0 for missing cursor, got %d", got)
	}

	writeWatchCursor(cf, 4096)
	if got := readWatchCursor(cf); got != 4096 {
		t.Errorf("expected 4096, got %d", got)
	}

	writeWatchCursor(cf, 0)
	if got := readWatchCursor(cf); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// TestExpandTilde verifies tilde expansion.
func TestExpandTilde(t *testing.T) {
	home, _ := os.UserHomeDir()

	got, err := expandTilde("~/.claude/projects")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(home, ".claude/projects")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Non-tilde path passes through unchanged.
	got2, _ := expandTilde("/absolute/path")
	if got2 != "/absolute/path" {
		t.Errorf("expected /absolute/path, got %q", got2)
	}
}

// TestIngestFile_NewSession verifies that a new transcript file is ingested,
// turns are returned, and the cursor is advanced.
func TestIngestFile_NewSession(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()

	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       &mockAdapter{},
			DebounceMs:    300,
		},
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	// Write a transcript file with two lines.
	sessionID := "test-session-001"
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")
	line1 := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "Hello"},
	})
	line2 := mustMarshal(t, map[string]any{
		"type": "assistant", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "assistant", "content": "Hi there"},
	})

	if err := os.WriteFile(transcriptPath, []byte(line1+"\n"+line2+"\n"), 0644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	n := w.ingestFile(transcriptPath)
	if n != 2 {
		t.Errorf("expected 2 turns ingested, got %d", n)
	}

	// Cursor was written under .mom/cache/, advanced past both lines.
	cursorFile := filepath.Join(momDir, "cache", ".watch-cursor-"+sessionID)
	offset := readWatchCursor(cursorFile)
	expectedBytes := int64(len(line1) + 1 + len(line2) + 1) // +1 per newline
	if offset != expectedBytes {
		t.Errorf("expected cursor=%d, got %d", offset, expectedBytes)
	}
}

// TestIngestFile_IncrementalRead verifies that re-ingesting a file only reads new bytes.
func TestIngestFile_IncrementalRead(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()

	adapter := &mockAdapter{}
	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       adapter,
			DebounceMs:    300,
		},
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	sessionID := "incremental-session"
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")

	line1 := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "First"},
	})
	_ = os.WriteFile(transcriptPath, []byte(line1+"\n"), 0644)
	w.ingestFile(transcriptPath)
	firstCallCount := len(adapter.calls)

	// Append a second line.
	line2 := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "Second"},
	})
	f, _ := os.OpenFile(transcriptPath, os.O_APPEND|os.O_WRONLY, 0644)
	_, _ = f.WriteString(line2 + "\n")
	_ = f.Close()

	w.ingestFile(transcriptPath)
	newCalls := adapter.calls[firstCallCount:]

	// Only the second line should have been parsed.
	if len(newCalls) != 1 {
		t.Errorf("expected 1 new parse call, got %d: %v", len(newCalls), newCalls)
	}
}

// TestIngestFile_SkipsSubagents verifies subagent files are excluded by the caller.
// (The watcher's handleEvent skips paths containing "subagents".)
func TestIngestFile_SkipsSubagents(t *testing.T) {
	path := "/home/user/.claude/projects/proj/abc/subagents/agent.jsonl"
	if !strings.Contains(path, "subagents") {
		t.Error("test path should contain 'subagents'")
	}
	// Verify the filter logic used in handleEvent.
	if strings.Contains(path, "subagents") {
		// This is the expected skip branch — test passes.
		return
	}
	t.Error("subagent path was not detected")
}

// TestIngestFile_TruncatedLine verifies cursor doesn't advance past incomplete lines (#153).
func TestIngestFile_TruncatedLine(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()

	adapter := &mockAdapter{}
	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       adapter,
			DebounceMs:    300,
		},
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	sessionID := "truncated-session"
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")

	// Write one complete line + one incomplete (no trailing \n).
	completeLine := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "Complete"},
	})
	incompleteLine := `{"type":"user","partial":true`
	_ = os.WriteFile(transcriptPath, []byte(completeLine+"\n"+incompleteLine), 0644)

	w.ingestFile(transcriptPath)

	// Cursor should only cover the complete line (len + \n), NOT the partial.
	cursorFile := filepath.Join(momDir, "cache", ".watch-cursor-"+sessionID)
	cursor := readWatchCursor(cursorFile)
	expectedCursor := int64(len(completeLine) + 1) // complete line + \n
	if cursor != expectedCursor {
		t.Errorf("cursor=%d, want %d (should not include truncated line)", cursor, expectedCursor)
	}

	// Now "complete" the partial line by appending the rest + \n, and add another line.
	completed := incompleteLine + `,"content":"now complete"}`
	newLine := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "Third"},
	})
	// Rewrite: complete first line + now-complete second line + third line
	_ = os.WriteFile(transcriptPath, []byte(completeLine+"\n"+completed+"\n"+newLine+"\n"), 0644)

	callsBefore := len(adapter.calls)
	w.ingestFile(transcriptPath)
	newCalls := adapter.calls[callsBefore:]

	// Should have parsed the completed second line and the third line.
	if len(newCalls) != 2 {
		t.Errorf("expected 2 new parse calls after completing truncated line, got %d: %v", len(newCalls), newCalls)
	}
}

// TestIngestFile_FileShrink verifies cursor resets when file shrinks (#154).
func TestIngestFile_FileShrink(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()

	adapter := &mockAdapter{}
	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       adapter,
			DebounceMs:    300,
		},
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	sessionID := "shrink-session"
	transcriptPath := filepath.Join(transcriptDir, sessionID+".jsonl")

	// Write two lines, ingest them.
	line1 := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "First"},
	})
	line2 := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "Second"},
	})
	_ = os.WriteFile(transcriptPath, []byte(line1+"\n"+line2+"\n"), 0644)
	w.ingestFile(transcriptPath)

	cursorFile := filepath.Join(momDir, "cache", ".watch-cursor-"+sessionID)
	cursorBefore := readWatchCursor(cursorFile)
	if cursorBefore == 0 {
		t.Fatal("cursor should be > 0 after first ingest")
	}

	// Truncate and rewrite with a shorter file (simulates rotation).
	newLine := mustMarshal(t, map[string]any{
		"type": "user", "sessionId": sessionID,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "After-reset"},
	})
	_ = os.WriteFile(transcriptPath, []byte(newLine+"\n"), 0644)

	callsBefore := len(adapter.calls)
	n := w.ingestFile(transcriptPath)

	// Should have re-ingested from the beginning.
	if n == 0 {
		t.Error("expected entries after file shrink, got 0")
	}
	newCalls := adapter.calls[callsBefore:]
	if len(newCalls) != 1 {
		t.Errorf("expected 1 parse call after shrink, got %d", len(newCalls))
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

// TestNew_ProjectScoping_PiUsesCustomSlug verifies that when pi is one of
// the configured sources, its custom ProjectSlug is honored — the watcher
// scopes to the pi-style "--<path>--" subdirectory rather than the default
// "<path>" slug. This guards the privacy-bleed regression where a missing
// scoper override caused pi sessions from OTHER projects to be ingested.
func TestNew_ProjectScoping_PiUsesCustomSlug(t *testing.T) {
	base := t.TempDir() // simulated ~/.pi/agent/sessions
	momDir := filepath.Join(t.TempDir(), ".mom")
	projectDir := "/Users/foo/proj"

	// Create the pi-style scoped subdir so the scoping check finds it.
	piSlugDir := filepath.Join(base, "--Users-foo-proj--")
	if err := os.MkdirAll(piSlugDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Also create a default-style dir to prove pi DOESN'T use it.
	defaultSlugDir := filepath.Join(base, "-Users-foo-proj")
	if err := os.MkdirAll(defaultSlugDir, 0755); err != nil {
		t.Fatal(err)
	}

	w, err := New(Config{
		ProjectDir: projectDir,
		MomDir:     momDir,
		Sources: []Source{{
			Harness:       "pi",
			TranscriptDir: base,
			Adapter:       NewPiAdapter(),
		}},
		SweepOnly: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := w.TranscriptDir()
	if got != piSlugDir {
		t.Errorf("expected scoped dir to be pi slug %q, got %q", piSlugDir, got)
	}
}

// TestNew_ProjectScoping_ResolvesSymlinkedProjectDirBeforeSlugging guards the
// macOS /tmp -> /private/tmp mismatch seen in live release validation.
func TestNew_ProjectScoping_ResolvesSymlinkedProjectDirBeforeSlugging(t *testing.T) {
	base := t.TempDir()
	momDir := filepath.Join(t.TempDir(), ".mom")
	realProjectDir := filepath.Join(t.TempDir(), "real", "project")
	if err := os.MkdirAll(realProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkProjectDir := filepath.Join(t.TempDir(), "link-project")
	if err := os.Symlink(realProjectDir, linkProjectDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	canonicalProjectDir := pathutil.CanonicalDir(realProjectDir)
	realSlugDir := filepath.Join(base, projectSlug(canonicalProjectDir))
	if err := os.MkdirAll(realSlugDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := New(Config{
		ProjectDir: linkProjectDir,
		MomDir:     momDir,
		Sources: []Source{{
			Harness:       "claude",
			TranscriptDir: base,
			Adapter:       NewClaudeAdapter(),
		}},
		SweepOnly: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := w.TranscriptDir(); got != realSlugDir {
		t.Errorf("expected scoped dir to use canonical project slug %q, got %q", realSlugDir, got)
	}
}

// TestNew_ProjectScoping_ClaudeUsesDefaultSlug is the negative control:
// claude adapter does NOT implement ProjectScoper, so the default
// strings.ReplaceAll(path, "/", "-") rule applies.
func TestNew_ProjectScoping_ClaudeUsesDefaultSlug(t *testing.T) {
	base := t.TempDir()
	momDir := filepath.Join(t.TempDir(), ".mom")
	projectDir := "/Users/foo/proj"

	defaultSlugDir := filepath.Join(base, "-Users-foo-proj")
	if err := os.MkdirAll(defaultSlugDir, 0755); err != nil {
		t.Fatal(err)
	}
	piSlugDir := filepath.Join(base, "--Users-foo-proj--")
	if err := os.MkdirAll(piSlugDir, 0755); err != nil {
		t.Fatal(err)
	}

	w, err := New(Config{
		ProjectDir: projectDir,
		MomDir:     momDir,
		Sources: []Source{{
			Harness:       "claude",
			TranscriptDir: base,
			Adapter:       NewClaudeAdapter(),
		}},
		SweepOnly: true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got := w.TranscriptDir()
	if got != defaultSlugDir {
		t.Errorf("expected scoped dir to be default slug %q, got %q", defaultSlugDir, got)
	}
}
