# 0019 — Schema Registry + governance level B

ADR 0018 establishes a canonical event schema with `family.subject.verb` names. That contract needs a home: a place where schemas live as data, a process for adding or changing them, and a runtime that knows what to do when an event arrives that doesn't match. This ADR defines that home (the Schema Registry under `events/registry/`) and chooses a governance level for it.

**Registry shape.** Each registered schema is a JSON file under `events/registry/schemas/<family>/<subject>.<verb>.json`. Filenames are the source of truth for event names — the loader rejects any file whose name does not match the `family.subject.verb` regex from ADR 0018. The directory tree is the registry. There is no separate index file; adding a schema means committing a JSON file in the right place.

Each schema declares required and optional fields, their types, and short descriptions. A minimal example for `capture.turn.observed`:

```json
{
  "name": "capture.turn.observed",
  "description": "A turn was observed on a harness transcript.",
  "fields": {
    "session_id":    {"type": "string", "required": true},
    "project_id":    {"type": "string", "required": false},
    "actor":         {"type": "string", "required": true, "values": ["user", "assistant", "tool"]},
    "text":          {"type": "string", "required": true},
    "tool_calls":    {"type": "array",  "required": false}
  }
}
```

The schema format is intentionally small. Anything richer (cross-field constraints, conditional requirements) is a future extension; v1 covers field names, types, required/optional, and bounded enums.

**Three levels of governance considered.**

- *Level A — strict runtime.* Every event is validated against its schema at publish time; unknown fields are rejected; missing required fields fail the publish. Tightest contract, highest friction.
- *Level B — reviewed schemas, permissive runtime.* Schemas are reviewed and validated in CI (filename regex, JSON well-formedness, taxonomy compliance). At runtime, events are validated against required fields, but unknown fields are kept and a warning is logged. Events with missing required fields are logged at error level but **never dropped silently** — they reach the bus with a synthetic `_schema_violation` marker.
- *Level C — schemas as documentation only.* Files exist but neither CI nor runtime consults them.

**MOM adopts level B.** The reasoning is that MOM is harness-agnostic and its capture path runs continuously across heterogeneous, sometimes-broken producers. Strict runtime rejection means a malformed turn — produced by a harness update we haven't caught up to — silently disappears from the memory layer. That is the failure mode MOM exists to prevent. Permissive runtime keeps the data; CI keeps the schemas honest; the combination gives reviewers leverage without making the runtime fragile.

**CI enforcement (level B's review half).** A `make verify-registry` (or equivalent CI step) runs on every PR that touches `events/registry/schemas/`:

1. Every file path matches the `family.subject.verb.json` regex.
2. Every file is well-formed JSON and parses against the registry's own schema-of-schemas.
3. No event name is removed without an accompanying deprecation marker (an additive-only history check, scoped to released names).

**Runtime enforcement (level B's permissive half).** The registry exposes `Validate(event) Result`. The Editor (ADR 0020) calls `Validate` on every event before publish:

- Missing required fields produce an error-level log and a marker field on the event; the event still publishes. Crier (ADR 0022) is free to skip projection of events carrying the marker, but never silently.
- Unknown fields produce a debug-level log on first occurrence per process and are otherwise passed through unchanged.
- Type mismatches on declared fields produce an error-level log and the offending field is dropped from the event before publish; the rest of the event still publishes with a marker.

The principle is **never lose data we already received, but make every deviation visible.** Operators reading Logbook see schema violations as first-class events; CI catches them at PR time; the runtime captures them as evidence rather than silently masking them.

**Schema evolution.** Adding a new optional field is a non-breaking change and ships in a single PR. Adding a new required field is breaking and requires a deprecation pass: the field becomes required only after one release in which the producer is updated and the registry warns on missing values. Removing a field is breaking and follows the same deprecation cadence. Renaming uses add-new + deprecate-old over two releases.

**`bootstrap` family.** Reserved but unused. The registry will refuse to load schemas under `events/registry/schemas/bootstrap/` until the cartographer revival lands.

## Consequences

- Schemas are reviewable as standalone files. A PR that adds `capture.turn.observed` is mechanically separable from the producer that emits it.
- The runtime never loses data because of a schema mismatch. Recall and Logbook keep working even when a producer drifts.
- Operators have a single place — schema violation logs — to see whether producers and the registry have diverged.
- Schema evolution is explicit. "Adding a required field" is a multi-PR exercise, on purpose.
- The registry has no dependency on the Vault or the Ledger. It is a pure data + validation library, importable from Editor, Crier, and tests.
- CI runs the registry loader on every PR that touches schemas. Bad files fail loud and fast.

## Considered alternatives

- **Level A — strict runtime.** Rejected as the headline failure mode is silent data loss when a harness update changes a payload shape we haven't yet registered. MOM's job is to keep the memory; a strict runtime fights that job.
- **Level C — documentation only.** Rejected: schemas that nothing consults rot in days. CI enforcement is the cheap half of level B and pays for itself the first time a typo would have shipped.
- **Single registry file (`registry.json` listing all schemas).** Rejected: turns every schema change into a merge-conflict surface. Per-file schemas let independent PRs land independently.
- **YAML schemas instead of JSON.** Rejected: events themselves are JSON-shaped, validators are JSON-native, and YAML's flexibility (anchors, multi-document streams) buys nothing here. JSON keeps tooling boring.
- **Generated Go types from schemas.** Rejected for v1: would couple the registry to a code-generation step and create two sources of truth (the JSON, the generated `.go`). Validation against `map[string]any` is fine for now; the codegen door stays open if consumers start asking for it.
- **Strict runtime with a feature flag.** Rejected: introducing a flag means we have to support both modes forever. The decision is the decision.
- **External registry service (HTTP server).** Rejected: MOM runs on the user's machine. Adding a network dependency to a local memory tool is the wrong shape.
- **Schemas as protobuf / Avro.** Rejected for v1: heavier tooling, more learning surface, and the gain (binary-stable wire format) is moot inside one process. JSON Schema-like declarations are sufficient.
- **Auto-register schemas at runtime from producer code.** Rejected: removes the review surface that's the whole point. Schemas are reviewed, then producers emit them — not the other way around.
- **No `_schema_violation` marker — just log and proceed.** Rejected: downstream consumers (especially Crier) need a programmatic signal, not just a log line, to decide whether to project an event.
