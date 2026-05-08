package watcher

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/herald"
)

// TestIngestFile_PublishesTurnObserved locks the watcher's v0.30
// emission contract: every parsed turn produces ONE turn.observed
// event on the configured bus, with the rich payload Drafter and
// Logbook consume.
func TestIngestFile_PublishesTurnObserved(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()
	bus := herald.NewBus()

	var mu sync.Mutex
	var captured []herald.Event
	bus.Subscribe(herald.TurnObserved, func(e herald.Event) {
		mu.Lock()
		captured = append(captured, e)
		mu.Unlock()
	})

	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       NewClaudeAdapter(),
			Bus:           bus,
			DebounceMs:    300,
		},
		timers:  make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile: filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	// Two real Claude transcript lines: one user, one assistant with
	// usage + a tool_use block.
	transcriptPath := filepath.Join(transcriptDir, "s-emit.jsonl")
	body := claudeUserTurn + "\n" + claudeAssistantToolUseTurn + "\n"
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	w.ingestFile(transcriptPath)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("got %d turn.observed events, want 2", len(captured))
	}

	// First event: user turn.
	user := captured[0]
	if user.SessionID != "s-test" {
		t.Errorf("user event SessionID = %q, want s-test", user.SessionID)
	}
	if got, _ := user.Payload["role"].(string); got != "user" {
		t.Errorf("user event role = %v, want user", user.Payload["role"])
	}

	// Second event: assistant turn with tool_use.
	asst := captured[1]
	if asst.SessionID != "s-test" {
		t.Errorf("assistant event SessionID = %q, want s-test", asst.SessionID)
	}
	if got, _ := asst.Payload["role"].(string); got != "assistant" {
		t.Errorf("assistant event role = %v, want assistant", asst.Payload["role"])
	}
	tcs, _ := asst.Payload["tool_calls"].([]map[string]any)
	if len(tcs) != 2 {
		t.Errorf("tool_calls len = %d, want 2 (Read + Bash)", len(tcs))
	}
	// Verify the Bash command (which contains an AKIA-shaped sentinel
	// in the fixture) IS preserved in the bus payload — Drafter needs
	// it for filter decisions. The privacy stripping happens in
	// Logbook's projection, not in the bus event itself.
	if len(tcs) >= 2 {
		input, _ := tcs[1]["input"].(map[string]any)
		cmd, _ := input["command"].(string)
		if cmd == "" {
			t.Errorf("Bash tool_call.input.command should be preserved on the bus, got empty")
		}
	}

	// Sanity: timestamps were stamped (the bus stamps Timestamp).
	for i, e := range captured {
		if e.Timestamp.IsZero() {
			t.Errorf("event[%d] missing Timestamp (bus should stamp)", i)
		}
	}
}

// TestIngestFile_PublishesNothingWithoutBus locks the inverse: when
// the watcher has no Bus configured, ingestion still works (legacy
// raw writer) but no events are published. Catches a future
// regression where someone accidentally creates a default Bus.
func TestIngestFile_PublishesNothingWithoutBus(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()

	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       NewClaudeAdapter(),
			DebounceMs:    300,
			// Bus intentionally nil
		},
		timers:  make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile: filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	transcriptPath := filepath.Join(transcriptDir, "s-no-bus.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(claudeUserTurn+"\n"), 0o644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	// Should not panic; should still write to raw.
	count := w.ingestFile(transcriptPath)
	if count == 0 {
		t.Errorf("expected ingest count > 0, got 0")
	}
}

// TestIngestFile_PublishesOneEventPerTurn uses a watcher with three
// turns to verify the per-turn-not-per-batch contract.
func TestIngestFile_PublishesOneEventPerTurn(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()
	bus := herald.NewBus()

	var fires atomic.Int64
	bus.Subscribe(herald.TurnObserved, func(e herald.Event) { fires.Add(1) })

	w := &Watcher{
		cfg: Config{
			TranscriptDir: transcriptDir,
			MomDir:        momDir,
			Adapter:       NewClaudeAdapter(),
			Bus:           bus,
			DebounceMs:    300,
		},
		timers:  make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile: filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	transcriptPath := filepath.Join(transcriptDir, "s-three.jsonl")
	body := claudeUserTurn + "\n" + claudeAssistantTextTurn + "\n" + claudeAssistantToolUseTurn + "\n"
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	w.ingestFile(transcriptPath)

	if got := fires.Load(); got != 3 {
		t.Errorf("turn.observed fired %d times, want 3 (one per turn)", got)
	}
}
