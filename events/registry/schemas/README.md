# Schema Registry

Canonical event schemas for MOM, per [ADR 0018](../../../adr/0018-canonical-event-schema.md) and [ADR 0019](../../../adr/0019-schema-registry-governance-b.md).

## Layout

```
events/registry/schemas/
├── capture/
│   ├── turn.observed.json
│   └── memory.recorded.json
├── lifecycle/
│   ├── draft.created.json
│   ├── draft.promoted.json
│   └── draft.expired.json
├── interaction/
│   ├── tool.called.json
│   └── tool.returned.json
└── operational/
    ├── daemon.started.json
    ├── daemon.stopped.json
    └── project.bound.json
```

The **filename** is the source of truth for the event name. The directory is the family. The composed name `<family>.<subject>.<verb>` must match:

```
^(capture|lifecycle|interaction|operational)\.[a-z0-9_]+\.[a-z0-9_]+$
```

The `bootstrap` family is reserved but not yet accepted (parked for Cartographer revival, #240).

## Schema document shape

```json
{
  "name": "capture.turn.observed",
  "description": "A turn was observed on a harness transcript.",
  "fields": {
    "session_id": {"type": "string", "required": true},
    "project_id": {"type": "string", "required": false},
    "actor":      {"type": "string", "required": true, "values": ["user", "assistant", "tool"]},
    "text":       {"type": "string", "required": true}
  }
}
```

- `name` MUST equal `<dir>.<filename without .json>`.
- `fields[*].type` ∈ `{string, number, bool, array, object}`.
- `fields[*].values` declares a bounded enum; only valid on `string`.
- Required-field violations + type mismatches + enum violations trigger the `_schema_violation` marker at runtime per [ADR 0019](../../../adr/0019-schema-registry-governance-b.md). Unknown fields are tolerated and warned once per process.

## Adding a new event

1. Decide the family. If your event doesn't fit `capture` / `lifecycle` / `interaction` / `operational`, write an ADR proposing a new family before adding the schema.
2. Create the JSON file at `events/registry/schemas/<family>/<subject>.<verb>.json`.
3. Run `make verify-registry` locally — it must pass before opening a PR.
4. In the same PR (or a follow-up), update the Editor's `Canonicalize` to emit this event, and add a Crier projection function.

## Adding a required field to an existing event

This is a **breaking** change. Land it in two PRs:

1. Add the field as **optional** in the schema; ship the producer.
2. After one release, flip the field to required.

## Renaming or removing an event

Two releases. Add the new event name, deprecate the old in the schema's `description`, and after one release remove the old schema file.

Removing without deprecation is caught by CI (`make verify-registry` runs an additive-only history check against the previous tag once that machinery lands; today the check is "the file exists at PR time").
