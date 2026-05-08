package logbook_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
)

// openLibWithLogbook opens a vault with both Librarian's and Logbook's
// migrations applied. Used by tests that exercise the op_events table.
func openLibWithLogbook(t *testing.T) *librarian.Librarian {
	t.Helper()
	dir := t.TempDir()
	migrations := append(librarian.Migrations(), logbook.Migrations()...)
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migrations)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return librarian.New(v)
}

func TestInsertOpEvent_RoundTrip(t *testing.T) {
	l := openLibWithLogbook(t)
	id, err := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.recall.queried",
		SessionID: "s-test-1",
		Payload:   map[string]any{"query": "deploy", "max_results": 10},
	})
	if err != nil {
		t.Fatalf("InsertOpEvent: %v", err)
	}
	if id <= 0 {
		t.Fatalf("InsertOpEvent returned id %d, want > 0", id)
	}

	rows, err := l.QueryOpEvents(librarian.OpEventFilter{})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	got := rows[0]
	if got.ID != id {
		t.Errorf("ID = %d, want %d", got.ID, id)
	}
	if got.EventType != "op.recall.queried" {
		t.Errorf("EventType = %q", got.EventType)
	}
	if got.SessionID != "s-test-1" {
		t.Errorf("SessionID = %q", got.SessionID)
	}
	if got.Payload["query"] != "deploy" {
		t.Errorf("payload.query = %v", got.Payload["query"])
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should default to now, got zero")
	}
}

func TestInsertOpEvent_RejectsEmptyEventType(t *testing.T) {
	l := openLibWithLogbook(t)
	_, err := l.InsertOpEvent(librarian.OpEvent{SessionID: "s"})
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestInsertOpEvent_RejectsEmptySessionID(t *testing.T) {
	l := openLibWithLogbook(t)
	_, err := l.InsertOpEvent(librarian.OpEvent{EventType: "op.x"})
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestInsertOpEvent_NilAndEmptyPayload_RoundTripIdentically(t *testing.T) {
	l := openLibWithLogbook(t)

	// Insert with nil payload.
	if _, err := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.x",
		SessionID: "s-nil",
		Payload:   nil,
	}); err != nil {
		t.Fatalf("Insert nil: %v", err)
	}
	// Insert with empty (non-nil) map payload.
	if _, err := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.x",
		SessionID: "s-empty",
		Payload:   map[string]any{},
	}); err != nil {
		t.Fatalf("Insert empty: %v", err)
	}

	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{EventType: "op.x"})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Payload != nil {
			t.Errorf("session %q: payload = %v, want nil (round-trip from null)", r.SessionID, r.Payload)
		}
	}
}

func TestQueryOpEvents_FilterByEventType(t *testing.T) {
	l := openLibWithLogbook(t)
	_, _ = l.InsertOpEvent(librarian.OpEvent{EventType: "op.a", SessionID: "s"})
	_, _ = l.InsertOpEvent(librarian.OpEvent{EventType: "op.b", SessionID: "s"})

	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{EventType: "op.a"})
	if len(rows) != 1 || rows[0].EventType != "op.a" {
		t.Fatalf("filter by event_type: got %v, want [op.a]", rows)
	}
}

func TestQueryOpEvents_FilterBySession(t *testing.T) {
	l := openLibWithLogbook(t)
	_, _ = l.InsertOpEvent(librarian.OpEvent{EventType: "op.x", SessionID: "s-1"})
	_, _ = l.InsertOpEvent(librarian.OpEvent{EventType: "op.x", SessionID: "s-2"})

	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{SessionID: "s-1"})
	if len(rows) != 1 || rows[0].SessionID != "s-1" {
		t.Fatalf("filter by session: got %v, want [s-1]", rows)
	}
}

func TestQueryOpEvents_FilterBySinceUntilWindow(t *testing.T) {
	l := openLibWithLogbook(t)
	t0 := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	for i, dt := range []time.Duration{
		0,                  // 10:00
		1 * time.Hour,      // 11:00
		2 * time.Hour,      // 12:00
		3 * time.Hour,      // 13:00
	} {
		_, err := l.InsertOpEvent(librarian.OpEvent{
			EventType: "op.x",
			SessionID: "s",
			CreatedAt: t0.Add(dt),
			Payload:   map[string]any{"i": i},
		})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{
		Since: t0.Add(1 * time.Hour),    // inclusive
		Until: t0.Add(3 * time.Hour),    // exclusive
	})
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows in [11:00, 13:00), got %d", len(rows))
	}
}

func TestQueryOpEvents_DefaultLimit_100(t *testing.T) {
	l := openLibWithLogbook(t)
	// Insert 105 rows with ascending timestamps so ID order matches
	// time order. The contract is "return the most recent 100 rows
	// in DESC order" — len==100 alone doesn't lock that, an ASC
	// regression would still pass len==100 but return the OLDEST
	// 100 instead.
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	ids := make([]int64, 105)
	for i := 0; i < 105; i++ {
		id, err := l.InsertOpEvent(librarian.OpEvent{
			EventType: "op.x",
			SessionID: "s",
			CreatedAt: t0.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
		ids[i] = id
	}
	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 100 {
		t.Fatalf("default limit: got %d rows, want 100", len(rows))
	}
	// Ordering: most recent first. rows[0] must be the row with the
	// LAST id (= 105th insert); rows[99] must be the 6th insert.
	if rows[0].ID != ids[104] {
		t.Errorf("rows[0].ID = %d, want %d (most recent)", rows[0].ID, ids[104])
	}
	if rows[99].ID != ids[5] {
		t.Errorf("rows[99].ID = %d, want %d (the 6th-oldest survives the limit)", rows[99].ID, ids[5])
	}
}

func TestQueryOpEvents_OrderingIsMostRecentFirst(t *testing.T) {
	l := openLibWithLogbook(t)
	t0 := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)
	earlier, _ := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.x", SessionID: "s", CreatedAt: t0,
	})
	later, _ := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.x", SessionID: "s", CreatedAt: t0.Add(time.Hour),
	})

	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].ID != later || rows[1].ID != earlier {
		t.Fatalf("order: got [%d, %d], want [%d (later), %d (earlier)]",
			rows[0].ID, rows[1].ID, later, earlier)
	}
}

// TestQueryOpEvents_MixedPrecisionLexicalOrdering locks the lesson from
// the previous attempt: timestamps with different fractional-second
// precision must lexically sort consistently with their actual time
// ordering. RFC3339Nano breaks this because it trims trailing zeros and
// "." < "Z" in ASCII. The fixed-width nanosecond format keeps sort
// correct.
func TestQueryOpEvents_MixedPrecisionLexicalOrdering(t *testing.T) {
	l := openLibWithLogbook(t)
	// One row at exactly 10:00:05Z (zero fractional part) and one at
	// 10:00:05.5Z (half-second past). Under RFC3339Nano the strings
	// would be "...:05Z" and "...:05.5Z"; "." (0x2E) sorts before "Z"
	// (0x5A), so the half-second-LATER row would sort BEFORE the
	// even-second row — wrong. The fixed-width format always emits
	// nine fractional digits ("...:05.000000000Z" vs
	// "...:05.500000000Z") so lexical sort matches time ordering.
	tBase := time.Date(2026, 5, 4, 10, 0, 5, 0, time.UTC)
	tHalf := tBase.Add(500 * time.Millisecond)

	earlierID, _ := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.x", SessionID: "s", CreatedAt: tBase,
	})
	laterID, _ := l.InsertOpEvent(librarian.OpEvent{
		EventType: "op.x", SessionID: "s", CreatedAt: tHalf,
	})

	rows, _ := l.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// Most recent first: tHalf (later) must appear before tBase
	// (earlier) — both purely from the lexical ordering of created_at.
	if rows[0].ID != laterID || rows[1].ID != earlierID {
		t.Fatalf("lexical order broken: got [%d, %d], want [later=%d, earlier=%d]",
			rows[0].ID, rows[1].ID, laterID, earlierID)
	}
}
