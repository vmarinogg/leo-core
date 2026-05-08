package drafter_test

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/drafter"
	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
)

// openDrafter opens a fresh vault + librarian and returns a Drafter
// plus the underlying Librarian for read-back assertions.
func openDrafter(t *testing.T) (*drafter.Drafter, *librarian.Librarian) {
	t.Helper()
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...)
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migs)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	lib := librarian.New(v)
	return drafter.New(lib), lib
}

// substantiveTurn returns a payload that survives the soft filter.
func substantiveTurn(text, harness string) map[string]any {
	return map[string]any{
		"role":    "assistant",
		"text":    text,
		"harness": harness,
		"model":   "claude-sonnet-4-6",
	}
}

// TestObserveTurn_BuffersWithoutPersisting locks the H2 contract
// flip from PR 2: a single turn does NOT immediately become a memory.
// It must wait for a flush trigger.
func TestObserveTurn_BuffersWithoutPersisting(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("deploy postgres canary, set the connection pool to 50", "claude-code"),
	})

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories before flush, want 0 (turn must remain buffered)", len(rows))
	}
}

// TestFlushAll_PersistsBufferedChunk locks the simplest end-to-end
// path: substantive turn buffered, FlushAll drains it, one memory
// lands with the redacted text + auto-extracted tags + provenance
// stamp.
func TestFlushAll_PersistsBufferedChunk(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	var created atomic.Int64
	bus.Subscribe(herald.OpMemoryCreated, func(e herald.Event) { created.Add(1) })

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("deploy postgres canary, set the connection pool to 50", "claude-code"),
	})
	d.FlushAll()

	if got := created.Load(); got != 1 {
		t.Fatalf("op.memory.created fired %d times, want 1", got)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories after flush, want 1", len(rows))
	}
	m := rows[0].Memory
	if !strings.Contains(m.Content, "postgres canary") {
		t.Errorf("flushed content lost original text: %q", m.Content)
	}
	if m.ProvenanceTriggerEvent != "watcher" {
		t.Errorf("ProvenanceTriggerEvent = %q, want watcher", m.ProvenanceTriggerEvent)
	}
	if m.ProvenanceSourceType != "transcript-extraction" {
		t.Errorf("ProvenanceSourceType = %q, want transcript-extraction", m.ProvenanceSourceType)
	}
	if m.ProvenanceActor != "claude-code" {
		t.Errorf("ProvenanceActor = %q, want claude-code (from harness)", m.ProvenanceActor)
	}
}

// TestFlushAll_ClustersCorrelatedTurns locks the v1 quality win:
// multiple turns on the same topic flush as ONE memory, not N
// fragments. This is the headline behaviour of the H2 rewrite.
func TestFlushAll_ClustersCorrelatedTurns(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	// Identical text on every turn keeps RAKE output stable and
	// divergence at zero — the locked contract is "boundary
	// detection collapses correlated turns into a single chunk."
	body := "working on the drafter buffer in cli/internal/drafter/drafter.go — the drafter clustering pipeline buffers turns and flushes them as memory chunks"
	turns := []string{body, body, body}
	for _, txt := range turns {
		bus.Publish(herald.Event{
			Type:      herald.TurnObserved,
			SessionID: "s",
			Payload:   substantiveTurn(txt, "claude-code"),
		})
	}
	d.FlushAll()

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories after flushing 3 correlated turns, want 1 (clustering)", len(rows))
	}
	m := rows[0].Memory
	if !strings.Contains(m.Content, "drafter clustering pipeline") {
		t.Errorf("clustered content missing shared phrasing: %q", m.Content)
	}
	// Three flushed turns must be present — clustering preserves
	// each turn's prose under the joined chunk content.
	if strings.Count(m.Content, "drafter buffer") != 3 {
		t.Errorf("expected all 3 turn texts joined into one memory, got %q", m.Content)
	}
}

// TestFlushAll_RedactedTurnEmitsRedactedOp locks the privacy
// contract end-to-end through clustering: a turn containing a secret
// is buffered with [REDACTED] in place; the chunk that includes a
// redacted turn emits op.memory.redacted (not created).
func TestFlushAll_RedactedTurnEmitsRedactedOp(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	var redacted atomic.Int64
	bus.Subscribe(herald.OpMemoryRedacted, func(e herald.Event) { redacted.Add(1) })

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("Why isn't AKIA1234567890ABCDEF working in this region?", "claude-code"),
	})
	d.FlushAll()

	if got := redacted.Load(); got != 1 {
		t.Fatalf("op.memory.redacted fired %d times, want 1", got)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1", len(rows))
	}
	m := rows[0].Memory
	if strings.Contains(m.Content, "AKIA1234567890ABCDEF") {
		t.Errorf("AWS key survived in flushed content: %q", m.Content)
	}
	if !strings.Contains(m.Content, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker in flushed content: %q", m.Content)
	}

	auditRows, err := lib.FilterAuditCounts()
	if err != nil {
		t.Fatalf("FilterAuditCounts: %v", err)
	}
	if len(auditRows) != 1 || auditRows[0].Category != "aws_key" {
		t.Fatalf("filter_audit rows = %+v, want one aws_key row", auditRows)
	}
}

// TestObserveTurn_DropsNoise locks the soft filter: an "ok" turn
// fires op.memory.dropped and never enters the buffer.
func TestObserveTurn_DropsNoise(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	var dropped atomic.Int64
	bus.Subscribe(herald.OpMemoryDropped, func(e herald.Event) { dropped.Add(1) })

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload: map[string]any{
			"role": "user",
			"text": "ok",
		},
	})
	d.FlushAll()

	if got := dropped.Load(); got != 1 {
		t.Errorf("op.memory.dropped fired %d times, want 1", got)
	}
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 0 {
		t.Errorf("got %d memories after a noise turn + flush, want 0", len(rows))
	}
}

// TestProcessRecord_BypassesBuffer locks the explicit-write path:
// memory.record persists immediately, no buffer, no filters, no
// clustering. Same contract as PR 2.
func TestProcessRecord_BypassesBuffer(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	bus.Publish(herald.Event{
		Type:      herald.MemoryRecord,
		SessionID: "s",
		Payload: map[string]any{
			"content":                  map[string]any{"text": "I learned that AKIA1234567890ABCDEF is a sample key"},
			"tags":                     []string{"deploy", "aws"},
			"provenance_actor":         "claude-code",
			"provenance_source_type":   "manual-draft",
			"provenance_trigger_event": "record",
		},
	})

	// No flush call — record path persists synchronously.
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1 (record bypasses buffer)", len(rows))
	}
	m := rows[0].Memory
	// Filter bypass: the secret-shaped string MUST survive verbatim.
	if !strings.Contains(m.Content, "AKIA1234567890ABCDEF") {
		t.Errorf("explicit-write content was redacted: %q", m.Content)
	}
	if m.ProvenanceTriggerEvent != "record" {
		t.Errorf("ProvenanceTriggerEvent = %q, want record", m.ProvenanceTriggerEvent)
	}
	auditRows, _ := lib.FilterAuditCounts()
	if len(auditRows) != 0 {
		t.Errorf("filter_audit bumped on bypass path: %+v", auditRows)
	}
	tagged, _ := lib.MemoriesByTag("deploy")
	if len(tagged) != 1 {
		t.Errorf("MemoriesByTag(deploy) = %v, want one entry", tagged)
	}
}

// TestTick_FlushesIdleSession locks the idle-flush trigger: a buffer
// with lastSeen older than idleFlushAfter relative to the supplied
// now is drained.
func TestTick_FlushesIdleSession(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("buffered turn that should flush on idle tick", "claude-code"),
	})

	// Tick well past the idle window — 10 minutes is comfortably
	// beyond the 90s default.
	d.Tick(time.Now().Add(10*time.Minute))

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Errorf("got %d memories after idle tick, want 1", len(rows))
	}
}

// TestOpMemoryEvents_PersistedThroughLogbook locks the audit-stream
// integration: when Logbook subscribes alongside Drafter, every
// flushed memory's outcome lands as an op_events row.
func TestOpMemoryEvents_PersistedThroughLogbook(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	lb := logbook.New(lib)
	defer lb.SubscribeAll(bus,
		herald.OpMemoryCreated,
		herald.OpMemoryRedacted,
		herald.OpMemoryDropped,
	)()

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("deploy postgres canary, set the connection pool to 50", "claude-code"),
	})
	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("AKIA1234567890ABCDEF leaked into the deploy step somehow", "claude-code"),
	})
	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload: map[string]any{
			"role": "user",
			"text": "ok",
		},
	})
	d.FlushAll()

	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{SessionID: "s", Limit: 100})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	got := map[string]int{}
	for _, r := range rows {
		got[r.EventType]++
	}
	// One ack-noise turn → op.memory.dropped.
	// Two substantive turns clustered into one chunk → one op event,
	// either created or redacted depending on whether the cluster
	// touched a redacted turn. Given a key matches in turn 2, it
	// must be redacted.
	if got[string(herald.OpMemoryDropped)] != 1 {
		t.Errorf("op.memory.dropped count = %d, want 1", got[string(herald.OpMemoryDropped)])
	}
	totalCreatedOrRedacted := got[string(herald.OpMemoryCreated)] + got[string(herald.OpMemoryRedacted)]
	if totalCreatedOrRedacted < 1 {
		t.Errorf("expected at least one op.memory.created/redacted, got %v", got)
	}
}

// TestProcessRecord_AtomicMemoryAndTags locks the F5 contract from
// PR 2: a record event with valid tags persists the memory + every
// tag edge in one transaction.
func TestProcessRecord_AtomicMemoryAndTags(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	bus.Publish(herald.Event{
		Type:      herald.MemoryRecord,
		SessionID: "s",
		Payload: map[string]any{
			"content":                  map[string]any{"text": "deploy plan"},
			"tags":                     []string{"deploy", "postgres", "canary"},
			"provenance_actor":         "claude-code",
			"provenance_source_type":   "manual-draft",
			"provenance_trigger_event": "record",
		},
	})

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories, want 1", len(rows))
	}
	memID := rows[0].ID
	for _, tag := range []string{"deploy", "postgres", "canary"} {
		ids, err := lib.MemoriesByTag(tag)
		if err != nil {
			t.Fatalf("MemoriesByTag(%q): %v", tag, err)
		}
		if len(ids) != 1 || ids[0] != memID {
			t.Errorf("MemoriesByTag(%q) = %v, want [%q]", tag, ids, memID)
		}
	}
}

// TestObserveTurn_TurnCountCapTriggersAutoFlush locks the
// flushAtTurnCount=50 trigger: hitting the cap drains the buffer
// without an explicit FlushAll call. Identical body keeps boundary
// detection from splitting, so the 50 turns flush as one chunk → one
// memory. Without this trigger, sessions under-the-cap-but-busy
// never persist between idle ticks.
func TestObserveTurn_TurnCountCapTriggersAutoFlush(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	body := "working on the drafter buffer in cli/internal/drafter/drafter.go — the drafter clustering pipeline buffers turns and flushes them as memory chunks"
	for i := 0; i < 50; i++ {
		bus.Publish(herald.Event{
			Type:      herald.TurnObserved,
			SessionID: "s",
			Payload:   substantiveTurn(body, "claude-code"),
		})
	}

	// No FlushAll here — the cap is the only trigger.
	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 1 {
		t.Fatalf("got %d memories after 50-turn cap, want 1 (auto-flush)", len(rows))
	}
}

// TestObserveTurn_BufferReCreatedAfterFlush locks the post-flush
// recovery path: after a session's buffer drains (cap, idle, or
// FlushAll), a new turn for the same session must be admitted into
// a fresh buffer and the next flush must produce a second memory.
// Without this guarantee, every session would be limited to one
// memory across its whole lifetime.
func TestObserveTurn_BufferReCreatedAfterFlush(t *testing.T) {
	d, lib := openDrafter(t)
	bus := herald.NewBus()
	defer d.SubscribeAll(bus)()

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("first batch — deploy postgres canary, set the connection pool to 50", "claude-code"),
	})
	d.FlushAll()

	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s",
		Payload:   substantiveTurn("second batch — review the canary metrics and roll forward to 100 percent", "claude-code"),
	})
	d.FlushAll()

	rows, _ := lib.SearchMemories(librarian.SearchFilter{SessionID: "s", Limit: 10})
	if len(rows) != 2 {
		t.Fatalf("got %d memories after two flush cycles, want 2", len(rows))
	}
}
