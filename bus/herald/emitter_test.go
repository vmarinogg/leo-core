package herald

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setNow overrides the package-level clock for the duration of the test.
func setNow(t *testing.T, ts time.Time) {
	t.Helper()
	orig := nowFn
	nowFn = func() time.Time { return ts }
	t.Cleanup(func() { nowFn = orig })
}

// readLines reads all JSONL lines from a file and decodes each as a
// map[string]any for assertion.
func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("parse line: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// newTmpEmitter creates an Emitter backed by a temp dir.
func newTmpEmitter(t *testing.T) (*Emitter, string) {
	t.Helper()
	momDir := t.TempDir()
	e := New(momDir, true)
	return e, filepath.Join(momDir, "logs")
}

// ── Round-trip tests (all 5 kinds) ─────────────────────────────────────────

func TestEmitSessionEvent(t *testing.T) {
	e, telDir := newTmpEmitter(t)
	day := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	setNow(t, day)

	e.EmitSessionEvent(SessionEvent{
		SessionID:     "s-abc",
		RepoID:        "mom",
		Harness:       "claude-code",
		StartedAt:     "2026-04-18T12:00:00Z",
		Trigger:       "normal",
		TurnCount:     12,
		ToolCallCount: 45,
	})

	lines := readLines(t, filepath.Join(telDir, "2026-04-18.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	got := lines[0]
	assertEqual(t, "kind", "SessionEvent", got["kind"])
	assertEqual(t, "session_id", "s-abc", got["session_id"])
	assertEqual(t, "trigger", "normal", got["trigger"])
	assertEqual(t, "turn_count", float64(12), got["turn_count"])
}

func TestEmitCaptureEvent(t *testing.T) {
	e, telDir := newTmpEmitter(t)
	day := time.Date(2026, 4, 18, 9, 0, 0, 0, time.UTC)
	setNow(t, day)

	e.EmitCaptureEvent(CaptureEvent{
		CaptureID:        "c-001",
		SessionID:        "s-abc",
		TS:               "2026-04-18T09:00:00Z",
		ExtractorModel:   "claude-sonnet-4.7",
		ExtractorVersion: "0.8.0",
		MemoriesProposed: 4,
		MemoriesAccepted: 3,
		Tags:             []string{"architecture", "auth"},
		Summary:          "refactored auth flow",
	})

	lines := readLines(t, filepath.Join(telDir, "2026-04-18.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	got := lines[0]
	assertEqual(t, "kind", "CaptureEvent", got["kind"])
	assertEqual(t, "capture_id", "c-001", got["capture_id"])
	assertEqual(t, "memories_accepted", float64(3), got["memories_accepted"])
}

func TestEmitMemoryMutation(t *testing.T) {
	e, telDir := newTmpEmitter(t)
	day := time.Date(2026, 4, 18, 10, 0, 0, 0, time.UTC)
	setNow(t, day)

	prev := "deadbeef"
	e.EmitMemoryMutation(MemoryMutation{
		MemoryID:       "m-001",
		Op:             "create",
		TS:             "2026-04-18T10:00:00Z",
		PrevHash:       &prev,
		NewHash:        "cafebabe",
		PromotionState: "draft",
		By:             "agent",
	})

	lines := readLines(t, filepath.Join(telDir, "2026-04-18.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	got := lines[0]
	assertEqual(t, "kind", "MemoryMutation", got["kind"])
	assertEqual(t, "op", "create", got["op"])
	assertEqual(t, "new_hash", "cafebabe", got["new_hash"])
}

func TestEmitConsumptionEvent(t *testing.T) {
	e, telDir := newTmpEmitter(t)
	day := time.Date(2026, 4, 18, 11, 0, 0, 0, time.UTC)
	setNow(t, day)

	sid := "s-abc"
	e.EmitConsumptionEvent(ConsumptionEvent{
		MemoryID:  "m-001",
		SessionID: &sid,
		TS:        "2026-04-18T11:00:00Z",
		ByAgent:   "claude-code",
		Context:   "prompt",
	})

	lines := readLines(t, filepath.Join(telDir, "2026-04-18.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	got := lines[0]
	assertEqual(t, "kind", "ConsumptionEvent", got["kind"])
	assertEqual(t, "by_agent", "claude-code", got["by_agent"])
	assertEqual(t, "context", "prompt", got["context"])
}

func TestEmitHarnessHealth(t *testing.T) {
	e, telDir := newTmpEmitter(t)
	day := time.Date(2026, 4, 18, 13, 0, 0, 0, time.UTC)
	setNow(t, day)

	e.EmitHarnessHealth(HarnessHealth{
		Harness:       "claude-code",
		TS:            "2026-04-18T13:00:00Z",
		WrapUpSuccess: true,
		LatencyMS:     420,
	})

	lines := readLines(t, filepath.Join(telDir, "2026-04-18.jsonl"))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	got := lines[0]
	assertEqual(t, "kind", "HarnessHealth", got["kind"])
	assertEqual(t, "wrap_up_success", true, got["wrap_up_success"])
	assertEqual(t, "latency_ms", float64(420), got["latency_ms"])
}

// ── Per-day rotation ────────────────────────────────────────────────────────

func TestDayRotation(t *testing.T) {
	e, telDir := newTmpEmitter(t)

	day1 := time.Date(2026, 4, 18, 23, 59, 59, 0, time.UTC)
	setNow(t, day1)
	e.EmitHarnessHealth(HarnessHealth{Harness: "claude-code", TS: "2026-04-18T23:59:59Z", WrapUpSuccess: true})

	day2 := time.Date(2026, 4, 19, 0, 0, 1, 0, time.UTC)
	nowFn = func() time.Time { return day2 }
	e.EmitHarnessHealth(HarnessHealth{Harness: "claude-code", TS: "2026-04-19T00:00:01Z", WrapUpSuccess: true})

	file1 := filepath.Join(telDir, "2026-04-18.jsonl")
	file2 := filepath.Join(telDir, "2026-04-19.jsonl")

	if _, err := os.Stat(file1); err != nil {
		t.Fatalf("expected %s: %v", file1, err)
	}
	if _, err := os.Stat(file2); err != nil {
		t.Fatalf("expected %s: %v", file2, err)
	}

	lines1 := readLines(t, file1)
	lines2 := readLines(t, file2)
	if len(lines1) != 1 {
		t.Fatalf("day1: expected 1 line, got %d", len(lines1))
	}
	if len(lines2) != 1 {
		t.Fatalf("day2: expected 1 line, got %d", len(lines2))
	}
}

// ── Disabled emitter ────────────────────────────────────────────────────────

func TestDisabledEmitter(t *testing.T) {
	momDir := t.TempDir()
	e := New(momDir, false)
	day := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	setNow(t, day)

	e.EmitHarnessHealth(HarnessHealth{Harness: "claude-code", TS: "2026-04-18T12:00:00Z", WrapUpSuccess: true})

	telDir := filepath.Join(momDir, "logs")
	entries, _ := os.ReadDir(telDir)
	if len(entries) != 0 {
		t.Fatalf("disabled emitter wrote %d file(s); expected 0", len(entries))
	}
}

// ── Error path: read-only directory ─────────────────────────────────────────

func TestReadOnlyDirNoPanic(t *testing.T) {
	momDir := t.TempDir()
	telDir := filepath.Join(momDir, "logs")
	if err := os.MkdirAll(telDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Make telemetry dir read-only so writes fail.
	if err := os.Chmod(telDir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(telDir, 0755) }) //nolint:errcheck

	e := &Emitter{dir: telDir, enabled: true}
	day := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	setNow(t, day)

	// Must not panic.
	e.EmitHarnessHealth(HarnessHealth{Harness: "claude-code", TS: "2026-04-18T12:00:00Z", WrapUpSuccess: false})
}

// ── Multiple events same day ─────────────────────────────────────────────────

func TestMultipleEventsAppend(t *testing.T) {
	e, telDir := newTmpEmitter(t)
	day := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	setNow(t, day)

	e.EmitHarnessHealth(HarnessHealth{Harness: "r1", TS: "T", WrapUpSuccess: true})
	e.EmitHarnessHealth(HarnessHealth{Harness: "r2", TS: "T", WrapUpSuccess: false})
	e.EmitSessionEvent(SessionEvent{SessionID: "s-1", Harness: "claude-code", Trigger: "normal"})

	lines := readLines(t, filepath.Join(telDir, "2026-04-18.jsonl"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

// ── helper ──────────────────────────────────────────────────────────────────

func assertEqual(t *testing.T, field string, want, got any) {
	t.Helper()
	if got != want {
		t.Errorf("field %q: want %v (%T), got %v (%T)", field, want, want, got, got)
	}
}
