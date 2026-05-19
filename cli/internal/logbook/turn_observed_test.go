package logbook_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
	"github.com/momhq/mom/cli/internal/watcher"
)

// TestSubscribeTurnObserved_PersistsMetadataProjection locks the
// privacy contract: when a turn.observed event arrives with raw text
// and tool inputs in the payload, the persisted op_events row contains
// ONLY role, tool_categories, privacy-safe tool_names, usage, model,
// provider. No text. No tool_call inputs.
//
// This is the most important test in PR 1 — it's the lock that
// prevents Drafter's redaction promise from being undone by the
// audit substrate.
func TestSubscribeTurnObserved_PersistsMetadataProjection(t *testing.T) {
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...); sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migs)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	lib := librarian.New(v)
	w := logbook.New(lib)
	bus := herald.NewBus()
	defer w.SubscribeTurnObserved(bus)()

	turnedAt := time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC)

	// Publish a turn that contains raw text AND a tool input with a
	// secret-shaped string that Drafter would normally redact.
	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s-test",
		Payload: map[string]any{
			"role":       "assistant",
			"created_at": turnedAt,
			"text":       "I'll deploy now using AKIA1234567890ABCDEF",
			"tool_calls": []map[string]any{
				{
					"name":      "Read",
					"safe_name": "Read",
					"category":  "codebase_read",
					"input":     map[string]any{"file_path": "/Users/x/secrets.env"},
				},
				{
					"name":      "Bash",
					"safe_name": "mom recall",
					"category":  "mom_cli",
					"input":     map[string]any{"command": "mom recall AKIA1234567890ABCDEF"},
				},
			},
			"usage": map[string]any{
				"input_tokens":       1234,
				"output_tokens":      56,
				"cache_read_tokens":  200,
				"cache_write_tokens": 100,
				"total_tokens":       1290,
				"cost_usd":           0.0185,
				"stop_reason":        "end_turn",
			},
			"model":    "claude-sonnet-4-6",
			"provider": "anthropic",
			"harness":  "claude-code",
		},
	})

	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{EventType: "turn.observed"})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	row := rows[0]

	// Required projection fields.
	if row.SessionID != "s-test" {
		t.Errorf("SessionID = %q", row.SessionID)
	}
	if got, _ := row.Payload["role"].(string); got != "assistant" {
		t.Errorf("role = %v", row.Payload["role"])
	}
	if got, _ := row.Payload["model"].(string); got != "claude-sonnet-4-6" {
		t.Errorf("model = %v", row.Payload["model"])
	}
	if got, _ := row.Payload["provider"].(string); got != "anthropic" {
		t.Errorf("provider = %v", row.Payload["provider"])
	}
	if got, _ := row.Payload["harness"].(string); got != "claude-code" {
		t.Errorf("harness = %v, want claude-code", row.Payload["harness"])
	}

	// CreatedAt: the persisted row's created_at must reflect the
	// turn-occurred time, not the bus publish time.
	gap := row.CreatedAt.Sub(turnedAt)
	if gap < -time.Second || gap > time.Second {
		t.Errorf("row.CreatedAt = %v, want close to %v (turn-occurred)", row.CreatedAt, turnedAt)
	}
	// created_at must NOT appear in the persisted payload.
	if _, present := row.Payload["created_at"]; present {
		t.Error("created_at leaked into the persisted payload; should be on OpEvent.CreatedAt only")
	}
	cats, _ := row.Payload["tool_categories"].([]any)
	if len(cats) != 2 {
		t.Errorf("tool_categories len = %d, want 2 (one per tool_call)", len(cats))
	}
	for i, want := range []string{"codebase_read", "mom_cli"} {
		if i < len(cats) {
			got, _ := cats[i].(string)
			if got != want {
				t.Errorf("tool_categories[%d] = %v, want %q", i, cats[i], want)
			}
		}
	}
	names, _ := row.Payload["tool_names"].([]any)
	if len(names) != 2 {
		t.Errorf("tool_names len = %d, want 2 (one per tool_call)", len(names))
	}
	for i, want := range []string{"Read", "mom recall"} {
		if i < len(names) {
			got, _ := names[i].(string)
			if got != want {
				t.Errorf("tool_names[%d] = %v, want %q", i, names[i], want)
			}
		}
	}

	// CRITICAL: privacy contract — content / tool_input must NOT be
	// in the persisted payload. Walk the entire payload as a string
	// blob and assert the secret never appears.
	dump := dumpPayload(row.Payload)
	if strings.Contains(dump, "AKIA") {
		t.Errorf("payload leaked the AWS-key sentinel:\n%s", dump)
	}
	if strings.Contains(dump, "secrets.env") {
		t.Errorf("payload leaked the file path:\n%s", dump)
	}
	if strings.Contains(dump, "deploy now") {
		t.Errorf("payload leaked the assistant text:\n%s", dump)
	}
	if strings.Contains(dump, "echo ") {
		t.Errorf("payload leaked the Bash command:\n%s", dump)
	}

	// tool_calls should NOT survive the projection; only safe tool_names do.
	if _, present := row.Payload["tool_calls"]; present {
		t.Error("tool_calls should not appear in the projection")
	}
	if _, present := row.Payload["text"]; present {
		t.Error("text should not appear in the projection")
	}
}

// dumpPayload renders the payload back to a deterministic string for
// substring assertions. We don't care about JSON validity — only
// "is this string anywhere in the persisted payload."
func dumpPayload(p map[string]any) string {
	var b strings.Builder
	walk(p, &b)
	return b.String()
}

func walk(v any, b *strings.Builder) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			b.WriteString(k)
			b.WriteString("=")
			walk(vv, b)
			b.WriteString(";")
		}
	case []any:
		for _, item := range x {
			walk(item, b)
			b.WriteString(",")
		}
	default:
		b.WriteString(toString(x))
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func TestSubscribeTurnObserved_DropsEventsWithoutSessionID(t *testing.T) {
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...); sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	v, _ := vault.Open(filepath.Join(dir, "mom.db"), migs)
	t.Cleanup(func() { _ = v.Close() })
	lib := librarian.New(v)
	w := logbook.New(lib)
	bus := herald.NewBus()
	defer w.SubscribeTurnObserved(bus)()

	// Empty SessionID — should be dropped (programming-error state).
	bus.Publish(herald.Event{
		Type: herald.TurnObserved,
		Payload: map[string]any{
			"role":  "assistant",
			"model": "claude-sonnet-4-6",
		},
	})

	rows, _ := lib.QueryOpEvents(librarian.OpEventFilter{})
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0 (empty session_id should drop)", len(rows))
	}
}

// TestSubscribeTurnObserved_PreservesHarnessFromTurnPayload locks the
// full chain that #340 fixed: when a watcher.Turn with Harness set
// flows through Turn.ToPayload() → herald.Event → Logbook, the
// persisted op_events.payload carries the harness.
//
// The existing PersistsMetadataProjection test builds the payload by
// hand (it always had "harness" in the map), so it never noticed that
// ToPayload was dropping the field. This test goes via ToPayload to
// verify the integration.
func TestSubscribeTurnObserved_PreservesHarnessFromTurnPayload(t *testing.T) {
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...)
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migs)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	lib := librarian.New(v)
	w := logbook.New(lib)
	bus := herald.NewBus()
	defer w.SubscribeTurnObserved(bus)()

	turn := watcher.Turn{
		SessionID: "s-harness",
		Role:      "user",
		Text:      "hello",
		Harness:   "pi",
	}
	bus.Publish(herald.Event{
		Type:      herald.TurnObserved,
		SessionID: turn.SessionID,
		Payload:   turn.ToPayload(),
	})

	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{EventType: "turn.observed"})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d op_events rows, want 1", len(rows))
	}
	got, _ := rows[0].Payload["harness"].(string)
	if got != "pi" {
		t.Errorf("op_events.payload.harness = %q, want pi", got)
	}
}
