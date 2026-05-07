// File worker.go contains Logbook's v0.30 surface: a worker that
// subscribes to operational events on Herald and persists them through
// Librarian into the op_events table. The legacy transcript-parsing
// surface in this package (logbook.go and friends) is a separate
// concern used by lens, watcher, and cmd; the two coexist while v1
// callers migrate.
package logbook

import (
	"fmt"
	"os"
	"time"

	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
)

// Worker subscribes to operational events on Herald and persists them
// through Librarian. Worker does NOT touch the Vault directly (that
// rule is owned by Librarian); the package-level architecture test
// asserts the import graph.
type Worker struct {
	lib *librarian.Librarian
}

// New returns a Worker backed by the given Librarian. Callers wire it
// to a Herald bus by calling Subscribe for each event type they want
// recorded.
func New(lib *librarian.Librarian) *Worker {
	return &Worker{lib: lib}
}

// Log writes an operational event directly through Librarian, without
// going via Herald. Useful for tests, for synchronous-write call paths
// (e.g., upgrade), and as the implementation hook used by Subscribe.
//
// EventType and SessionID are required; empty inputs are rejected at
// the API boundary by Librarian.
func (w *Worker) Log(eventType, sessionID string, payload map[string]any) error {
	_, err := w.lib.InsertOpEvent(librarian.OpEvent{
		EventType: eventType,
		SessionID: sessionID,
		Payload:   payload,
	})
	return err
}

// Subscribe registers a handler on the bus that persists every matching
// event. Returns the unsubscribe func from Herald — callers may detach
// the worker without retaining the Bus reference.
//
// SessionID comes from the Event envelope (e.SessionID), set by the
// producer. An empty SessionID is a programming-error event and is
// skipped with a stderr log — the schema NOT NULL constraint would
// reject it anyway, but losing it silently in the audit substrate is
// the bigger sin. A persistence failure (closed vault, FK error, disk
// full) is also logged to stderr; we do not silently drop audit data.
func (w *Worker) Subscribe(bus *herald.Bus, eventType herald.EventType) func() {
	return bus.Subscribe(eventType, func(e herald.Event) {
		if e.SessionID == "" {
			fmt.Fprintf(os.Stderr, "logbook: drop %q event with empty session_id\n", e.Type)
			return
		}
		if err := w.Log(string(e.Type), e.SessionID, e.Payload); err != nil {
			fmt.Fprintf(os.Stderr, "logbook: persist %q failed: %v\n", e.Type, err)
		}
	})
}

// SubscribeTurnObserved wires the worker to the watcher's
// `turn.observed` source-of-truth events. Unlike Subscribe (which
// persists the full payload), this method projects the payload to a
// metadata-only shape before persisting:
//
//	role             ("user" | "assistant")
//	tool_categories  ([]string, derived from tool_calls[].category)
//	tool_names       ([]string, privacy-safe tool names only, no args)
//	usage            (token counts only, no text)
//	model, provider, harness
//
// Raw text and tool inputs from the bus event are NEVER persisted.
// Drafter is responsible for capturing redacted memories from the
// same bus event; Logbook only records the audit trail.
//
// The persisted row's `created_at` reflects when the turn HAPPENED,
// not when the watcher saw it. The watcher parses the source
// timestamp into the bus payload's `created_at` key; Logbook lifts
// it onto the OpEvent.CreatedAt field so Lens timelines match the
// transcript even when ingestion runs in catch-up sweep mode.
//
// Empty SessionID drops the event with a stderr log (programming-
// error state). Persistence failures also log to stderr.
func (w *Worker) SubscribeTurnObserved(bus *herald.Bus) func() {
	return bus.Subscribe(herald.TurnObserved, func(e herald.Event) {
		if e.SessionID == "" {
			fmt.Fprintf(os.Stderr, "logbook: drop %q event with empty session_id\n", e.Type)
			return
		}
		projected, createdAt := projectTurnObserved(e.Payload)
		if _, err := w.lib.InsertOpEvent(librarian.OpEvent{
			EventType: string(e.Type),
			SessionID: e.SessionID,
			CreatedAt: createdAt, // zero falls through to InsertOpEvent's now() default
			Payload:   projected,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "logbook: persist %q failed: %v\n", e.Type, err)
		}
	})
}

// projectTurnObserved drops content (text, tool inputs) and keeps
// only what Lens renders. Returns the projection map and the
// turn-occurred timestamp (zero if absent). The caller lifts the
// timestamp onto OpEvent.CreatedAt so the persisted row reflects
// when the turn happened, not when the watcher published the event.
func projectTurnObserved(payload map[string]any) (map[string]any, time.Time) {
	if payload == nil {
		return nil, time.Time{}
	}
	out := map[string]any{}
	if role, _ := payload["role"].(string); role != "" {
		out["role"] = role
	}
	if model, _ := payload["model"].(string); model != "" {
		out["model"] = model
	}
	if provider, _ := payload["provider"].(string); provider != "" {
		out["provider"] = provider
	}
	if harness, _ := payload["harness"].(string); harness != "" {
		out["harness"] = harness
	}

	// tool_calls is []map[string]any in turn.ToPayload's output;
	// tolerate []any too in case the value round-tripped through JSON.
	if cats := extractToolCategories(payload["tool_calls"]); len(cats) > 0 {
		out["tool_categories"] = cats
	}
	if names := extractToolNames(payload["tool_calls"]); len(names) > 0 {
		out["tool_names"] = names
	}

	// Usage map: keep numeric fields only.
	if usage, ok := payload["usage"].(map[string]any); ok && usage != nil {
		out["usage"] = projectUsage(usage)
	}

	// created_at is consumed by the caller (lifted onto
	// OpEvent.CreatedAt); it does NOT appear in the persisted payload.
	var createdAt time.Time
	if v, ok := payload["created_at"]; ok {
		if t, ok := v.(time.Time); ok {
			createdAt = t
		}
	}
	return out, createdAt
}

func extractToolCategories(v any) []string {
	return extractToolStringField(v, "category")
}

func extractToolNames(v any) []string {
	return extractToolStringField(v, "safe_name")
}

func extractToolStringField(v any, field string) []string {
	switch tcs := v.(type) {
	case []map[string]any:
		out := make([]string, 0, len(tcs))
		for _, tc := range tcs {
			if s, _ := tc[field].(string); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(tcs))
		for _, item := range tcs {
			tc, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if s, _ := tc[field].(string); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// projectUsage retains only numeric token-accounting fields and the
// stop_reason string. Unknown keys are dropped.
func projectUsage(usage map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{
		"input_tokens",
		"output_tokens",
		"cache_read_tokens",
		"cache_write_tokens",
		"total_tokens",
		"cost_usd",
	} {
		if v, ok := usage[key]; ok {
			out[key] = v
		}
	}
	if reason, _ := usage["stop_reason"].(string); reason != "" {
		out["stop_reason"] = reason
	}
	return out
}

// SubscribeAll wires the worker to every listed event type and returns
// a single unsubscribe that detaches them all.
func (w *Worker) SubscribeAll(bus *herald.Bus, eventTypes ...herald.EventType) func() {
	unsubs := make([]func(), 0, len(eventTypes))
	for _, t := range eventTypes {
		unsubs = append(unsubs, w.Subscribe(bus, t))
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}

// Query reads back rows from the operational stream through Librarian.
func (w *Worker) Query(filter librarian.OpEventFilter) ([]librarian.OpEvent, error) {
	return w.lib.QueryOpEvents(filter)
}
