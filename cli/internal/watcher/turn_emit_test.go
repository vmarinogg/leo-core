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
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
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
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
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
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
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

// TestTurn_ToPayload_EmitsProjectId verifies ProjectId rides on the
// Herald payload when set (per ADR 0016).
func TestTurn_ToPayload_EmitsProjectId(t *testing.T) {
	turn := Turn{
		SessionID: "s",
		Role:      "user",
		Text:      "hi",
		ProjectId: "alpha",
	}
	p := turn.ToPayload()
	if p["project_id"] != "alpha" {
		t.Errorf("payload[project_id] = %v, want alpha", p["project_id"])
	}
}

// When ProjectId is empty, the payload omits the key entirely (so
// downstream consumers can tell "stamped as alpha" from "no stamp").
func TestTurn_ToPayload_OmitsProjectIdWhenEmpty(t *testing.T) {
	turn := Turn{SessionID: "s", Role: "user", Text: "hi"}
	p := turn.ToPayload()
	if _, ok := p["project_id"]; ok {
		t.Errorf("payload should omit project_id when empty, got %v", p["project_id"])
	}
}

// TestIngestFile_StampsProjectIdFromBindFile verifies that when the
// watcher's ProjectDir contains a .mom-project.yaml, every published
// Turn carries the resolved project_id (per ADR 0016).
func TestIngestFile_StampsProjectIdFromBindFile(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()
	projectDir := t.TempDir()
	// Write .mom-project.yaml at project root.
	if err := os.WriteFile(filepath.Join(projectDir, ".mom-project.yaml"),
		[]byte("version: \"1\"\nid: alpha\n"), 0o644); err != nil {
		t.Fatalf("write bind file: %v", err)
	}

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
			ProjectDir:    projectDir,
			MomDir:        momDir,
			Adapter:       NewClaudeAdapter(),
			Bus:           bus,
		},
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	transcriptPath := filepath.Join(transcriptDir, "s-pid.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(claudeUserTurn+"\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	w.ingestFile(transcriptPath)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("got %d events, want 1", len(captured))
	}
	got, _ := captured[0].Payload["project_id"].(string)
	if got != "alpha" {
		t.Errorf("payload project_id = %q, want alpha", got)
	}
}

// When the watcher's ProjectDir has no bind file, payloads omit
// project_id (NULL stamps downstream).
func TestIngestFile_OmitsProjectIdWhenUnbound(t *testing.T) {
	transcriptDir := t.TempDir()
	momDir := t.TempDir()
	projectDir := t.TempDir() // no .mom-project.yaml

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
			ProjectDir:    projectDir,
			MomDir:        momDir,
			Adapter:       NewClaudeAdapter(),
			Bus:           bus,
		},
		timers:    make(map[string]*time.Timer),
		cursorDir: filepath.Join(momDir, "cache"),
		logFile:   filepath.Join(momDir, "watch.log"),
	}
	_ = os.MkdirAll(w.cursorDir, 0755)

	transcriptPath := filepath.Join(transcriptDir, "s-unbound.jsonl")
	_ = os.WriteFile(transcriptPath, []byte(claudeUserTurn+"\n"), 0o644)
	w.ingestFile(transcriptPath)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("got %d events, want 1", len(captured))
	}
	if _, ok := captured[0].Payload["project_id"]; ok {
		t.Errorf("payload should omit project_id when unbound, got %v", captured[0].Payload["project_id"])
	}
}

// TestTurn_ToPayload_EmitsHarness locks the contract that watcher
// adapters' harness identity (claude-code, codex, pi, …) rides on the
// herald payload so Logbook and Drafter can attribute the turn to the
// right harness. Pre-#340 ToPayload silently dropped this field.
func TestTurn_ToPayload_EmitsHarness(t *testing.T) {
	turn := Turn{
		SessionID: "s",
		Role:      "user",
		Text:      "hi",
		Harness:   "pi",
	}
	p := turn.ToPayload()
	if p["harness"] != "pi" {
		t.Errorf("payload[harness] = %v, want pi", p["harness"])
	}
}

// When Harness is empty, payload omits the key — keeps the bus clean
// for paths that don't carry harness information.
func TestTurn_ToPayload_OmitsHarnessWhenEmpty(t *testing.T) {
	turn := Turn{SessionID: "s", Role: "user", Text: "hi"}
	p := turn.ToPayload()
	if _, ok := p["harness"]; ok {
		t.Errorf("payload should omit harness when empty, got %v", p["harness"])
	}
}
