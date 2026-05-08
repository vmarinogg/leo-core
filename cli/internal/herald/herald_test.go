package herald

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/archtest"
)

// readDir wraps os.ReadDir and returns the entries (ignoring errors gracefully).
func readDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

// readJSONLFile reads all JSONL lines from path, decoding each as map[string]any.
func readJSONLFile(t *testing.T, path string) []map[string]any {
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

// ── EventType constants ───────────────────────────────────────────────────────

func TestEventTypeConstants_AllDefined(t *testing.T) {
	types := []EventType{
		SessionStart,
		SessionEnd,
		TurnComplete,
		ToolUse,
		CompactTriggered,
		MemoryCreated,
		MemoryPromoted,
		MemorySearched,
		MemoryDeleted,
		RecordAppended,
		ConfigChanged,
		Error,
	}
	if len(types) != 12 {
		t.Fatalf("expected 12 event type constants, got %d", len(types))
	}
}

func TestEventTypeConstants_Unique(t *testing.T) {
	types := []EventType{
		SessionStart,
		SessionEnd,
		TurnComplete,
		ToolUse,
		CompactTriggered,
		MemoryCreated,
		MemoryPromoted,
		MemorySearched,
		MemoryDeleted,
		RecordAppended,
		ConfigChanged,
		Error,
	}
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %q", et)
		}
		seen[et] = true
	}
}

// ── Subscribe + Publish ───────────────────────────────────────────────────────

func TestPublish_DeliversToHandler(t *testing.T) {
	bus := NewBus()
	var received []Event

	bus.Subscribe(SessionStart, func(e Event) {
		received = append(received, e)
	})

	bus.Publish(Event{Type: SessionStart, SessionID: "s-123"})

	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	ev := received[0]
	if ev.Type != SessionStart {
		t.Errorf("expected type %q, got %q", SessionStart, ev.Type)
	}
	if ev.SessionID != "s-123" {
		t.Errorf("expected session_id %q, got %q", "s-123", ev.SessionID)
	}
	if ev.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestPublish_MultipleSubscribersReceiveSameEvent(t *testing.T) {
	bus := NewBus()
	var mu sync.Mutex
	var count int

	for i := 0; i < 3; i++ {
		bus.Subscribe(MemoryCreated, func(e Event) {
			mu.Lock()
			count++
			mu.Unlock()
		})
	}

	bus.Publish(Event{Type: MemoryCreated, Payload: map[string]any{"id": "m-001"}})

	if count != 3 {
		t.Errorf("expected 3 handler calls, got %d", count)
	}
}

func TestPublish_UnsubscribedEventType_NoOp(t *testing.T) {
	bus := NewBus()
	// No subscribers registered.
	// Must not panic, must not error.
	bus.Publish(Event{Type: ConfigChanged, Payload: map[string]any{"key": "telemetry"}})
}

func TestPublish_SetsTimestampUTC(t *testing.T) {
	bus := NewBus()
	var ev Event
	bus.Subscribe(TurnComplete, func(e Event) { ev = e })

	before := time.Now().UTC()
	bus.Publish(Event{Type: TurnComplete})
	after := time.Now().UTC()

	if ev.Timestamp.Before(before) || ev.Timestamp.After(after) {
		t.Errorf("timestamp %v not in expected range [%v, %v]", ev.Timestamp, before, after)
	}
	if ev.Timestamp.Location() != time.UTC {
		t.Errorf("expected UTC, got %v", ev.Timestamp.Location())
	}
}

func TestPublish_NilPayload_NoOp(t *testing.T) {
	bus := NewBus()
	var received Event
	bus.Subscribe(Error, func(e Event) { received = e })
	bus.Publish(Event{Type: Error})
	if received.Payload != nil {
		t.Errorf("expected nil payload, got %v", received.Payload)
	}
}

// ── Concurrent safety ─────────────────────────────────────────────────────────

func TestPublish_ConcurrentSafety(t *testing.T) {
	bus := NewBus()
	var count atomic.Int64

	bus.Subscribe(ToolUse, func(e Event) {
		count.Add(1)
	})

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			bus.Publish(Event{Type: ToolUse, Payload: map[string]any{"tool": "read"}})
		}()
	}
	wg.Wait()

	if count.Load() != goroutines {
		t.Errorf("expected %d events, got %d", goroutines, count.Load())
	}
}

func TestSubscribe_ConcurrentWithPublish(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	// Concurrent subscribes and publishes must not race.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Subscribe(MemorySearched, func(e Event) {})
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Publish(Event{Type: MemorySearched})
		}()
	}
	wg.Wait()
}

// ── TelemetrySubscriber ───────────────────────────────────────────────────────

func TestTelemetrySubscriber_WritesJSONLOnEvent(t *testing.T) {
	momDir := t.TempDir()
	bus := NewBus()
	ts := NewTelemetrySubscriber(momDir, true)
	ts.Register(bus)

	// Publish a known event; TelemetrySubscriber should write it.
	bus.Publish(Event{
		Type:      SessionStart,
		SessionID: "s-test",
		Payload:   map[string]any{"runtime": "claude-code"},
	})

	// Read back — there should be at least one JSONL file.
	telPath := momDir + "/logs"
	entries, err := readDir(telPath)
	if err != nil {
		t.Fatalf("cannot read telemetry dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one JSONL file, got none")
	}

	lines := readJSONLFile(t, telPath+"/"+entries[0].Name())
	if len(lines) == 0 {
		t.Fatal("expected at least one line in JSONL file")
	}

	first := lines[0]
	if len(first) == 0 {
		t.Error("JSONL line is empty map")
	}
}

func TestTelemetrySubscriber_DisabledWritesNothing(t *testing.T) {
	momDir := t.TempDir()
	bus := NewBus()
	ts := NewTelemetrySubscriber(momDir, false)
	ts.Register(bus)

	bus.Publish(Event{Type: SessionEnd, SessionID: "s-off"})

	telDir := momDir + "/telemetry"
	entries, _ := readDir(telDir)
	if len(entries) != 0 {
		t.Errorf("disabled subscriber wrote %d file(s), expected 0", len(entries))
	}
}

// ── v0.30 contract: type-only routing, unsubscribe, panic isolation ─────────

func TestPublish_TypeOnlyRouting(t *testing.T) {
	bus := NewBus()
	var aCount, bCount atomic.Int64

	bus.Subscribe(SessionStart, func(e Event) { aCount.Add(1) })
	bus.Subscribe(SessionEnd, func(e Event) { bCount.Add(1) })

	bus.Publish(Event{Type: SessionStart})
	bus.Publish(Event{Type: SessionStart})
	bus.Publish(Event{Type: SessionEnd})

	if got := aCount.Load(); got != 2 {
		t.Errorf("SessionStart handler got %d events, want 2", got)
	}
	if got := bCount.Load(); got != 1 {
		t.Errorf("SessionEnd handler got %d events, want 1", got)
	}
}

func TestSubscribe_ReturnsUnsubscribe_StopsDelivery(t *testing.T) {
	bus := NewBus()
	var count atomic.Int64

	unsub := bus.Subscribe(MemoryCreated, func(e Event) { count.Add(1) })

	bus.Publish(Event{Type: MemoryCreated})
	if got := count.Load(); got != 1 {
		t.Fatalf("got %d, want 1 (before unsubscribe)", got)
	}

	unsub()

	bus.Publish(Event{Type: MemoryCreated})
	if got := count.Load(); got != 1 {
		t.Errorf("got %d, want 1 (handler should not fire after unsubscribe)", got)
	}
}

func TestUnsubscribe_IsIdempotent(t *testing.T) {
	bus := NewBus()
	var count atomic.Int64

	unsub := bus.Subscribe(ToolUse, func(e Event) { count.Add(1) })

	// Calling unsubscribe twice must not panic and must not affect other
	// subscribers registered for the same type.
	bus.Subscribe(ToolUse, func(e Event) { count.Add(10) })

	unsub()
	unsub() // second call is a no-op

	bus.Publish(Event{Type: ToolUse})
	if got := count.Load(); got != 10 {
		t.Errorf("got %d, want 10 (only the still-subscribed handler should fire)", got)
	}
}

func TestUnsubscribe_OnlyAffectsTheReturnedHandler(t *testing.T) {
	bus := NewBus()
	var aCount, bCount atomic.Int64

	unsubA := bus.Subscribe(MemoryPromoted, func(e Event) { aCount.Add(1) })
	bus.Subscribe(MemoryPromoted, func(e Event) { bCount.Add(1) })

	unsubA()
	bus.Publish(Event{Type: MemoryPromoted})

	if a := aCount.Load(); a != 0 {
		t.Errorf("unsubscribed handler fired %d times", a)
	}
	if b := bCount.Load(); b != 1 {
		t.Errorf("other handler fired %d times, want 1", b)
	}
}

func TestPublish_HandlerPanicDoesNotBlockOthers(t *testing.T) {
	bus := NewBus()
	var beforeCount, afterCount atomic.Int64

	bus.Subscribe(Error, func(e Event) { beforeCount.Add(1) })
	bus.Subscribe(Error, func(e Event) { panic("handler exploded") })
	bus.Subscribe(Error, func(e Event) { afterCount.Add(1) })

	// Publish must not propagate the panic and must call handlers
	// registered after the panicking one.
	bus.Publish(Event{Type: Error, Payload: map[string]any{"msg": "test"}})

	if got := beforeCount.Load(); got != 1 {
		t.Errorf("before-panic handler got %d, want 1", got)
	}
	if got := afterCount.Load(); got != 1 {
		t.Errorf("after-panic handler got %d, want 1 — fan-out was blocked by the panic", got)
	}
}

func TestPublish_HandlerPanicAcrossMultiplePublishes(t *testing.T) {
	// A panicking handler should not deregister itself or break the bus
	// for future publishes.
	bus := NewBus()
	var fireCount atomic.Int64
	bus.Subscribe(ConfigChanged, func(e Event) {
		fireCount.Add(1)
		panic("boom")
	})

	for i := 0; i < 5; i++ {
		bus.Publish(Event{Type: ConfigChanged})
	}

	if got := fireCount.Load(); got != 5 {
		t.Errorf("handler fired %d times, want 5 (panic must not deregister it)", got)
	}
}

func TestSubscribe_ConcurrentUnsubscribeIsRaceFree(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unsub := bus.Subscribe(TurnComplete, func(e Event) {})
			unsub()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Publish(Event{Type: TurnComplete})
		}()
	}
	wg.Wait()
}

// TestSubscribeAndPublish_RejectEmptyEventType locks the boundary
// guard against a producer or subscriber that forgets to set Type.
// Without the guard the empty key becomes a silent black hole.
func TestSubscribeAndPublish_RejectEmptyEventType(t *testing.T) {
	bus := NewBus()
	var fires atomic.Int64

	// A subscriber on a real type that should NEVER fire from an
	// empty-Type publish.
	bus.Subscribe(SessionStart, func(e Event) { fires.Add(1) })

	// Empty subscribe returns a noop unsub but doesn't register.
	unsub := bus.Subscribe("", func(e Event) { fires.Add(100) })
	unsub() // no-op, must not panic

	// Empty publish is dropped; no handler fires.
	bus.Publish(Event{Type: "", SessionID: "s"})
	if got := fires.Load(); got != 0 {
		t.Errorf("empty Type publish fired handlers (count=%d); want 0", got)
	}

	// Real publish still works.
	bus.Publish(Event{Type: SessionStart, SessionID: "s"})
	if got := fires.Load(); got != 1 {
		t.Errorf("real publish fired %d times; want 1", got)
	}
}

// TestHerald_NoVaultOrLibrarianDependency enforces PRD 0003: Herald
// is a pure pub/sub bus and must not import Vault or Librarian.
// Herald's only imports are stdlib, so a direct-import check is
// sufficient — there's no transitive path in production code.
func TestHerald_NoVaultOrLibrarianDependency(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/cli/internal/vault",
		"github.com/momhq/mom/cli/internal/librarian",
	)
}
