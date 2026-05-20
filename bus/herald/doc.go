// Package herald is the unified event bus for MOM. It provides pub/sub
// event dispatch (Bus) and a JSONL telemetry writer (TelemetrySubscriber).
//
// Telemetry is NEVER memory. Events are written to append-only JSONL files
// (.mom/telemetry/YYYY-MM-DD.jsonl, UTC day rotation) and are never indexed,
// recalled, or placed in .mom/memory/.
//
// Herald replaces the former transponder package (v0.10). All existing
// telemetry emission methods (Emitter) are preserved for backward compatibility.
//
// Events are consumed by `mom doctor` and future Enterprise Dashboard.
// No network, no sync, no remote gateway — local only.
package herald
