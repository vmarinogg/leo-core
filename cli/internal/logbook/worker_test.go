package logbook_test

import (
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/momhq/mom/cli/internal/archtest"
	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
)

// openWorker opens a Vault with Librarian + Logbook migrations and
// returns a Logbook Worker plus the underlying Librarian for read-back.
func openWorker(t *testing.T) (*logbook.Worker, *librarian.Librarian) {
	t.Helper()
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...)
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migs)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	lib := librarian.New(v)
	return logbook.New(lib), lib
}

func TestWorker_Log_PersistsThroughLibrarian(t *testing.T) {
	w, lib := openWorker(t)
	if err := w.Log("op.session.started", "s-1", map[string]any{"runtime": "claude-code"}); err != nil {
		t.Fatalf("Log: %v", err)
	}

	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].EventType != "op.session.started" {
		t.Errorf("EventType = %q", rows[0].EventType)
	}
}

func TestWorker_Log_RejectsEmptyEventType(t *testing.T) {
	w, _ := openWorker(t)
	err := w.Log("", "s", nil)
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestWorker_Log_RejectsEmptySessionID(t *testing.T) {
	w, _ := openWorker(t)
	err := w.Log("op.x", "", nil)
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestWorker_Subscribe_PersistsPublishedEvents(t *testing.T) {
	w, lib := openWorker(t)
	bus := herald.NewBus()
	unsub := w.Subscribe(bus, "op.recall.queried")
	defer unsub()

	bus.Publish(herald.Event{
		Type:      "op.recall.queried",
		SessionID: "s-test",
		Payload:   map[string]any{"query": "deploy", "max_results": 10},
	})

	rows, _ := lib.QueryOpEvents(librarian.OpEventFilter{EventType: "op.recall.queried"})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].SessionID != "s-test" {
		t.Errorf("SessionID = %q, want s-test", rows[0].SessionID)
	}
	if rows[0].Payload["query"] != "deploy" {
		t.Errorf("payload.query = %v", rows[0].Payload["query"])
	}
}

func TestWorker_Subscribe_SkipsEventsWithoutSessionID(t *testing.T) {
	// Events without an envelope SessionID violate the API contract.
	// Rather than letting the row land with empty session_id (which is
	// also rejected at the API), the subscriber drops the event and
	// logs to stderr — empty session_id is always a programming error
	// and should never appear in the stream.
	w, lib := openWorker(t)
	bus := herald.NewBus()
	defer w.Subscribe(bus, "op.broken")()

	bus.Publish(herald.Event{
		Type:    "op.broken",
		Payload: map[string]any{"foo": "bar"},
	})

	rows, _ := lib.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0 (event without session_id should be skipped)", len(rows))
	}
}

func TestWorker_Unsubscribe_StopsRecording(t *testing.T) {
	w, lib := openWorker(t)
	bus := herald.NewBus()
	unsub := w.Subscribe(bus, "op.x")

	bus.Publish(herald.Event{Type: "op.x", SessionID: "s"})
	unsub()
	bus.Publish(herald.Event{Type: "op.x", SessionID: "s"})

	rows, _ := lib.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 1 {
		t.Fatalf("got %d rows after unsubscribe, want 1 (the pre-unsub event)", len(rows))
	}
}

func TestWorker_SubscribeAll_AndCombinedUnsubscribe(t *testing.T) {
	w, lib := openWorker(t)
	bus := herald.NewBus()
	stop := w.SubscribeAll(bus, "op.a", "op.b", "op.c")

	bus.Publish(herald.Event{Type: "op.a", SessionID: "s"})
	bus.Publish(herald.Event{Type: "op.b", SessionID: "s"})
	bus.Publish(herald.Event{Type: "op.c", SessionID: "s"})
	bus.Publish(herald.Event{Type: "op.d", SessionID: "s"}) // not subscribed

	rows, _ := lib.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (only a/b/c subscribed)", len(rows))
	}

	stop()
	bus.Publish(herald.Event{Type: "op.a", SessionID: "s"})
	rows, _ = lib.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 3 {
		t.Fatalf("got %d rows after combined unsub, want still 3", len(rows))
	}
}

// TestWorker_Subscribe_FanOutWithOtherSubscribers ensures Logbook does
// not interfere with other handlers on the same Herald event type.
func TestWorker_Subscribe_FanOutWithOtherSubscribers(t *testing.T) {
	w, _ := openWorker(t)
	bus := herald.NewBus()
	defer w.Subscribe(bus, "op.x")()

	var sibling atomic.Int64
	bus.Subscribe("op.x", func(e herald.Event) { sibling.Add(1) })

	bus.Publish(herald.Event{Type: "op.x", SessionID: "s"})

	if got := sibling.Load(); got != 1 {
		t.Errorf("sibling handler fired %d times, want 1", got)
	}
}

// TestLogbook_DoesNotImportVault enforces the architectural rule:
// Logbook is a worker that goes through Librarian; only Librarian
// touches Vault. Direct-import only — transitive (logbook → librarian
// → vault) is expected.
func TestLogbook_DoesNotImportVault(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/cli/internal/vault",
	)
}
