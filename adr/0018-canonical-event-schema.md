# 0018 — Canonical event schema

MOM ingests events from heterogeneous sources: Claude Code, Codex, and Pi watchers emit per-harness turn structures; the CLI emits explicit-record and lifecycle events; the daemon emits operational events. Today, every producer hand-rolls its payload shape and publishes directly onto Herald (`bus/herald`). `Turn.ToPayload()` (the current canonicalization-by-method) maps adapter-specific fields into a `map[string]any` that consumers parse defensively. Adding a producer means inventing keys; adding a consumer means guessing which keys are present. The `EventType` constants in Herald are strings with no naming discipline — `TurnObserved`, `MemoryRecord`, `OpMemoryCreated` — and no schema is attached to any of them.

This ADR establishes a canonical event schema: every event published past the Ingress boundary conforms to a registered schema, identified by a name that follows the convention `family.subject.verb`.

**Families v1.** Four families are introduced; a fifth is parked.

- `capture` — the data MOM exists to remember. Turns observed from harnesses, memories recorded explicitly, drafts created by the Drafter.
- `lifecycle` — state transitions on memories MOM already holds. Drafts promoted, drafts expired, memories curated, landmarks set.
- `interaction` — agent ↔ tool exchanges that may carry meaningful context. Tool calls and tool returns, distinct from the turn that envelopes them.
- `operational` — events about MOM itself. Daemon started/stopped, project bound, vault upgraded, filter audit incremented.
- `bootstrap` — parked. Cartographer-driven seeding is on hold (see #240); when it returns the family is reserved.

Family choice is a domain decision: "what kind of thing is this event about?" Reviewers can answer that for any new event without consulting a flowchart. The hybrid families + nested naming gives us coarse grouping for routing and fine grouping for evolution.

**Subject + verb.** Inside a family, an event names its subject and the action that happened to it. `capture.turn.observed`, `capture.memory.recorded`, `lifecycle.draft.promoted`, `interaction.tool.called`, `operational.daemon.started`. The full name is `^(capture|lifecycle|interaction|operational)\.[a-z0-9_]+\.[a-z0-9_]+$` — a regex small enough to live in CI and in the registry loader.

**Self-sufficient events.** A canonical event row in the Ledger must contain every field needed to project it. The Crier (ADR 0022) must be able to take any single event in isolation and produce the corresponding Vault state without consulting other events, the harness, or external context. This forecloses the "look up the turn this tool call belonged to" pattern that would couple events into chains and make replay order-dependent. If two events need to share data, both carry it; the registry validates the duplication.

**The `herald.Event` envelope is reused.** Today's `herald.Event` (`bus/herald/herald.go`) already carries `Type EventType`, `SessionID string`, `Timestamp time.Time`, and `Payload map[string]any`. This ADR formalizes what `Type` is (a registered schema name) and what `Payload` contains (fields the schema declares). The envelope shape does not change; this ADR is a contract layered on top of an existing primitive, not a new type system.

**Where canonicalization happens.** The Editor (ADR 0020) is the single gate between Ingress and the bus. Watcher adapters, CLI subcommands, and the MCP server hand raw input to the Editor; the Editor produces a canonical `herald.Event` and the Editor alone calls `bus.Publish`. No raw adapter type crosses the bus boundary.

**Migration from `herald.EventType` constants.** Each existing constant in `herald.go` (`TurnObserved`, `MemoryRecord`, `OpMemoryCreated`, etc.) maps to exactly one `family.subject.verb` name. The Item 1 implementation introduces the new names, registers their schemas, and keeps the old constants as deprecated aliases for one release so external consumers (e.g. third-party bus subscribers, if any) have a window to migrate.

## Consequences

- Every event that crosses the bus is now a known shape. Consumers stop parsing defensively; they parse against a schema.
- Adding a producer is a registry change plus an Editor mapping. The schema is reviewable on its own; the producer change is the implementation of an already-agreed contract.
- Replay is well-defined. Crier loads events from the Ledger and projects them in isolation; no event depends on another event's existence in the projection step.
- Family routing becomes cheap. Subscribers can filter by family prefix (e.g. "all `capture.*` events") without enumerating every event type.
- The `bootstrap` family is reserved but unused; documenting it now prevents the cartographer revival from inventing a parallel taxonomy.

## Considered alternatives

- **Flat event-name vocabulary (no families).** Rejected: works for ten events, fails at fifty. The `family.subject.verb` convention costs nothing today and prevents reviewers from arguing over whether `tool_called` is closer to `turn_observed` or to `daemon_started`.
- **Hierarchical event types as Go types (one struct per event).** Rejected for v1: would either fragment the bus into N typed channels or push consumers into type-switches. Keeping `herald.Event` with a `Payload map[string]any` plus schema validation lets the bus stay generic while still being safe at the validation boundary.
- **Strict-runtime validation (reject unknown fields).** Rejected. This ADR formalises the schema; ADR 0019 chooses governance level B (CI-reviewed, permissive runtime). The permissiveness is intentional and discussed there.
- **Producer-side event versioning (`capture.turn.observed.v2`).** Rejected for v1: version-as-suffix is one option among several (registry-internal schema versions, additive evolution, etc.). The registry (ADR 0019) is the right place to choose; this ADR leaves the name space clean.
- **Chained events (`tool.called` carries a reference back to the parent `turn.observed`).** Rejected: violates self-sufficiency. If the projector needs to combine information from two events, both events carry it; the registry catches the duplication at review time. Replay must work on a single event in isolation.
- **Reuse adapter-specific types as canonical (e.g. promote `watcher.Turn` to be the canonical capture event).** Rejected: locks the canonical schema to whichever harness drove its evolution. Editor canonicalization (ADR 0020) is the explicit decoupling step.
- **Open string vocabularies modelled on provenance (ADR 0014).** Rejected here: provenance is a tagging axis where new values appear over time and are intentionally not enforced. Event types are protocol shapes; consumers need them stable enough to parse. The two are different concerns and the registry treats them differently.
- **Skip the `interaction` family and roll tool events into `capture`.** Rejected: tool calls and returns are not turns. A single turn can spawn many tool calls; merging them collapses cardinality and makes recall queries ambiguous. Separate family, separate schemas.
