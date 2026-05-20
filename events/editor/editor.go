// Package editor is the canonicalization gateway between Ingress and
// the bus (ADR 0020). Every ingress surface — ingress/cli, ingress/mcp,
// ingress/watcher/adapters/* — hands its raw input to the Editor; the
// Editor produces a canonical herald.Event, validates it against the
// Schema Registry (ADR 0019), stamps provenance + project_id, and
// publishes to the bus.
//
// The Editor is the architectural invariant ADR 0020 enforces: no
// raw adapter type crosses the bus boundary. archtests in events/editor
// and bus/herald (added in #364) enforce this at PR time.
//
// In v0.50 the Editor sits between Ingress and bus only. In #366
// (Item 2.2) it gains a Ledger-append step ordered before the bus
// publish, making the Ledger the canonical record and the bus a
// projection.
package editor

import (
	"fmt"
	"log"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/events/registry"
	"github.com/momhq/mom/shared/project"
)

// Bus is the in-process publishing surface the Editor uses. Defined
// as an interface so tests can substitute a recorder and so the
// Editor doesn't transitively pull herald-test dependencies.
type Bus interface {
	Publish(herald.Event)
}

// LedgerAppender is the Ledger's Append surface, narrowed to what
// the Editor needs. Implementations: storage/ledger.Ledger (production),
// test recorders. Defined here so the Editor can stay decoupled from
// the concrete Ledger driver shape.
type LedgerAppender interface {
	Append(herald.Event) (uint64, error)
}

// Source carries the contextual metadata about *where* an input came
// from. Editor uses it to stamp provenance and (per ADR 0016) resolve
// project_id from cwd.
type Source struct {
	// Adapter names the ingress surface — "claude", "codex", "pi",
	// "cli", "mcp". Stamped onto the event as provenance_actor when
	// the payload does not already declare one.
	Adapter string

	// Cwd is the working directory active when the input was produced.
	// Used to resolve project_id via .mom-project.yaml walk-up. Empty
	// disables resolution; existing payload project_id is preserved.
	Cwd string
}

// Canonicalizer is implemented by inputs that know their canonical
// (eventType, payload) shape. The Editor calls Canonical() to extract
// the substance, then layers provenance, project_id, and validation
// on top.
//
// Producers (watcher.Turn, record.Request, …) implement this rather
// than the Editor type-switching on every known input. Adding a new
// input is a producer-side change; the Editor's contract is stable.
type Canonicalizer interface {
	Canonical() (eventType herald.EventType, payload map[string]any)
}

// Editor is the canonicalization gateway. Construct via New.
type Editor struct {
	bus      Bus
	ledger   LedgerAppender    // nil → Ledger append skipped (transitional)
	registry *registry.Registry // nil → validation skipped (transitional)
	logger   *log.Logger
}

// New constructs an Editor bound to bus and reg. If reg is nil, the
// Editor skips schema validation (useful during the v0.50 transition
// before #363 registers schemas). Ledger append is opt-in via
// WithLedger; absent that, the Editor publishes only onto the bus.
func New(bus Bus, reg *registry.Registry, logger *log.Logger) *Editor {
	if logger == nil {
		logger = log.Default()
	}
	return &Editor{bus: bus, registry: reg, logger: logger}
}

// WithLedger returns a new Editor wired to ledger. Production callers
// use this to enable the ADR 0021 durable-append path: every published
// event is appended to the Ledger before reaching the bus. Returns the
// receiver for fluent chaining.
func (e *Editor) WithLedger(ledger LedgerAppender) *Editor {
	e.ledger = ledger
	return e
}

// Canonicalize composes a canonical herald.Event from in + src without
// publishing. Pure (modulo logger side-effects). Used by tests and by
// Publish.
//
// Behaviour:
//  1. Call in.Canonical() to get eventType + payload.
//  2. If payload lacks provenance_actor and src.Adapter is non-empty,
//     stamp it.
//  3. If payload lacks project_id and src.Cwd is non-empty, resolve
//     via shared/project and stamp the result (if any).
//  4. Validate against the registry (if any). Level-B violations
//     (missing required, type mismatch, enum violation) attach a
//     _schema_violation field to the payload but never block publish.
//  5. Build the herald.Event envelope (Type, SessionID from payload,
//     Payload). Timestamp is set by herald.Publish, not here.
func (e *Editor) Canonicalize(in Canonicalizer, src Source) herald.Event {
	eventType, payload := in.Canonical()
	if payload == nil {
		payload = map[string]any{}
	}

	// Stamp provenance_actor if absent.
	if _, ok := payload["provenance_actor"]; !ok && src.Adapter != "" {
		payload["provenance_actor"] = src.Adapter
	}

	// Resolve project_id from cwd if absent.
	if _, ok := payload["project_id"]; !ok && src.Cwd != "" {
		if id, _, _, err := project.ResolveProject(src.Cwd); err == nil && id != "" {
			payload["project_id"] = id
		}
	}

	// Validate. Level-B: never drop; mark on violation.
	if e.registry != nil {
		if res := e.registry.Validate(string(eventType), payload); res.Marker() {
			payload["_schema_violation"] = encodeViolation(res)
			e.logger.Printf("editor: schema violation for %s: missing=%v mismatches=%v enums=%v",
				eventType, res.MissingFields, res.TypeMismatches, res.EnumViolations)
		}
	}

	sessionID, _ := payload["session_id"].(string)
	return herald.Event{
		Type:      eventType,
		SessionID: sessionID,
		Payload:   payload,
	}
}

// Publish is the production entry point. The order is fixed per
// ADR 0021 §crash-safety:
//
//  1. Canonicalize the input.
//  2. Append the canonical event to the Ledger (when wired). If
//     append fails, the event is NOT published onto the bus — the
//     caller observes the error and the bus stays consistent.
//  3. Publish onto the bus.
//
// Editors without a Ledger (transitional builds, tests) skip step 2.
// Editors without a Bus skip step 3.
//
// The promise: when Publish returns nil, the event is durably in the
// Ledger (if wired). A crash between Ledger append and bus publish
// leaves the event on Layer 1; Crier reprojects on restart (#367/#368).
func (e *Editor) Publish(in Canonicalizer, src Source) error {
	ev := e.Canonicalize(in, src)
	if e.ledger != nil {
		if _, err := e.ledger.Append(ev); err != nil {
			return fmt.Errorf("editor: ledger append %s: %w", ev.Type, err)
		}
	}
	if e.bus != nil {
		e.bus.Publish(ev)
	}
	return nil
}

// encodeViolation builds the _schema_violation marker payload. The
// shape is deliberately simple — Crier (#367) and Logbook can read it
// programmatically; humans reading Lens see a small object with the
// three violation categories.
func encodeViolation(res registry.Result) map[string]any {
	out := map[string]any{}
	if len(res.MissingFields) > 0 {
		out["missing_required"] = append([]string(nil), res.MissingFields...)
	}
	if len(res.TypeMismatches) > 0 {
		tm := make([]map[string]any, 0, len(res.TypeMismatches))
		for _, t := range res.TypeMismatches {
			tm = append(tm, map[string]any{"field": t.Field, "want": t.Want, "got": t.Got})
		}
		out["type_mismatches"] = tm
	}
	if len(res.EnumViolations) > 0 {
		ev := make([]map[string]any, 0, len(res.EnumViolations))
		for _, e := range res.EnumViolations {
			ev = append(ev, map[string]any{"field": e.Field, "value": e.Value, "want": e.Want})
		}
		out["enum_violations"] = ev
	}
	return out
}
