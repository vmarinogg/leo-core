# Changelog

All notable releases of MOM. Format derives from [Keep a Changelog](https://keepachangelog.com); the source of truth for design decisions is the [ADR series](adr/) and the [PRD series](prd/).

## v0.50.0-alpha — Post-Atomic architecture

The first release of the post-Atomic architecture. Three locked items from [PRD 0004](prd/0004-v0-50.md) ship together.

### Item 6 — Role-based repo layout
- The codebase is reorganised from `cli/internal/<32 packages>` into eight role-based top-level buckets: `events/`, `storage/`, `ingress/`, `bus/`, `workers/`, `services/`, `ops/`, `shared/` ([ADR 0017](adr/0017-role-based-repo-layout.md)).
- Module path drops the `/cli` suffix; now `github.com/momhq/mom`.
- `shared/archtest/layout_test.go` pins the layout so no future PR re-introduces `cli/internal/`.
- Dead packages removed: `recall/` (superseded by `finder/`), `diagnose/` (no consumer).

### Item 1 — Canonical event schema
- New `events/registry/` package with on-disk JSON schemas under `events/registry/schemas/<family>/<subject>.<verb>.json` ([ADR 0018](adr/0018-canonical-event-schema.md), [ADR 0019](adr/0019-schema-registry-governance-b.md)).
- Governance level B: CI validates filenames + structure; runtime stays permissive (missing required + type mismatches attach a `_schema_violation` marker but never block publish).
- New `events/editor/` package: the sole canonicalization gateway between Ingress and the bus ([ADR 0020](adr/0020-editor-canonicalization-gateway.md)). Stamps provenance + project_id, validates against the registry.
- `herald.EventType` constant *values* renamed to `family.subject.verb`: `capture.turn.observed`, `capture.memory.recorded`, `lifecycle.memory.{created,redacted,dropped}`.
- Schemas registered for every production-emitted event type.
- archtest invariants enforce: bus does not import ingress; editor does not import vault or workers or ingress adapter packages.

### Item 2 — Dual storage
- New `storage/ledger/` package: append-only canonical event log at `$HOME/.mom/ledger/`, length-prefixed JSON segments, `O_APPEND` only, fsync on every append, partial-write detection on reopen ([ADR 0021](adr/0021-ledger-layer-1-canonical-log.md)).
- New `events/crier/` package: idempotent projector that reads the Ledger and writes the Vault via Librarian ([ADR 0022](adr/0022-crier-projector-via-librarian.md)).
- Migration v7: `op_events.ledger_offset` + UNIQUE index, `crier_state` checkpoint table.
- Editor.Publish ordering: canonicalize → Ledger.Append + fsync → bus.Publish. Append failure aborts publish — the bus never sees an event whose canonical record isn't durably persisted.
- Replay test harness: full reprojection from offset 0 against a fresh vault reproduces the same state for every registered schema.
- archtest invariants: Crier does not import `storage/vault` directly; `services/finder` and `services/lens` do not import `storage/ledger` (read path stays CLI → Finder → Librarian → Vault).

### MCP transport deprecation
- MCP server still functions in v0.50 but emits a boot warning announcing the v0.60+ retirement ([ADR 0023](adr/0023-mcp-server-retirement.md)).
- CLI parity gaps closed: new `mom get <id>` and `mom landmarks [--limit N]` subcommands mirror `mom_get` and `mom_landmarks`.
- `ingress/mcp/parity_test.go` enforces: every MCP tool has a documented CLI counterpart at PR time.

### Process
- Single big-bang reorg PR (Phase 1) instead of incremental moves.
- All 16 milestone issues closed.
- New `release-gate` CI workflow: tag-push asserts zero open issues in the milestone — prevents silent spill into v0.60.
- New `pr-closes-link` CI workflow: every PR body must contain a `Closes #N` / `Fixes #N` / `Resolves #N` line so merge auto-closes the paired issue.

### Deferred to v0.60+
- Item 3 — Memory retention (Gardener, global TTL, hard delete, drafts only).
- Recall overhaul (hybrid query + UX).
- MCP server actual removal.
- Bootstrap (Cartographer) family + revival.

### Dropped
- Item 4 — LLM query-plan boundary.

### Internal-only references
The `mom-engineer/` documentation tree stays outside this repo. Every ADR and PRD in v0.50 is self-contained — decisions are restated inline so external readers need no internal access.
