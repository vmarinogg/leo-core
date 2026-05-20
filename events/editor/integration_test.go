package editor_test

import (
	"path/filepath"
	"testing"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/events/editor"
	"github.com/momhq/mom/storage/ledger"
)

// TestPublish_E2E_RealLedger appends through a real Ledger driver and
// confirms the on-disk record matches what reached the bus. This is
// the v0.50 baseline for "synthetic turn produces (a) ledger row +
// (b) same downstream Vault state as before" from #366's acceptance.
func TestPublish_E2E_RealLedger(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	led, err := ledger.Open(dir)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	defer led.Close()

	bus := &recordingBus{}
	e := editor.New(bus, nil, nil).WithLedger(led)

	in := staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "e2e", "text": "hello world", "role": "user"},
	}
	if err := e.Publish(in, editor.Source{Adapter: "claude-code"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Bus saw it.
	if len(bus.events) != 1 {
		t.Fatalf("bus.events len = %d, want 1", len(bus.events))
	}
	if bus.events[0].Type != herald.TurnObserved {
		t.Errorf("bus Type = %q, want %q", bus.events[0].Type, herald.TurnObserved)
	}

	// Ledger has it durably.
	rec, err := led.Read(0)
	if err != nil {
		t.Fatalf("ledger.Read(0): %v", err)
	}
	if rec.Event.Type != herald.TurnObserved {
		t.Errorf("ledger Type = %q, want %q", rec.Event.Type, herald.TurnObserved)
	}
	if got := rec.Event.Payload["text"]; got != "hello world" {
		t.Errorf("ledger payload text = %v, want hello world", got)
	}
	if got := rec.Event.Payload["provenance_actor"]; got != "claude-code" {
		t.Errorf("ledger payload provenance_actor = %v, want claude-code", got)
	}
}

// TestPublish_E2E_CrashRecovery simulates a crash between Ledger
// append and bus publish: append succeeds, bus.Publish is never
// reached (we abort). On the next process restart, Crier (when
// wired in #367) re-projects from the Ledger offset and produces
// the same downstream Vault state. This PR just verifies the
// Ledger retains the event when the bus path failed.
func TestPublish_E2E_CrashRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ledger")
	led, err := ledger.Open(dir)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}

	// Use a bus that panics — simulates the "crash after ledger,
	// before bus" window. We catch the panic via defer/recover.
	panicBus := &panickingBus{}
	e := editor.New(panicBus, nil, nil).WithLedger(led)

	func() {
		defer func() { _ = recover() }() // swallow simulated crash
		_ = e.Publish(staticInput{
			eventType: "capture.turn.observed",
			payload:   map[string]any{"session_id": "crash-test"},
		}, editor.Source{Adapter: "claude-code"})
	}()
	_ = led.Close()

	// Reopen the Ledger — event must still be there.
	led2, err := ledger.Open(dir)
	if err != nil {
		t.Fatalf("ledger.Open after crash: %v", err)
	}
	defer led2.Close()
	rec, err := led2.Read(0)
	if err != nil {
		t.Fatalf("ledger.Read(0) after crash: %v", err)
	}
	if rec.Event.SessionID != "crash-test" {
		t.Errorf("session = %q, want crash-test (event must survive crash)", rec.Event.SessionID)
	}
}

type panickingBus struct{}

func (p *panickingBus) Publish(_ herald.Event) {
	panic("simulated crash between ledger append and bus publish")
}
