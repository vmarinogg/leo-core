# 0022 — Crier as projector/replayer via Librarian

ADR 0021 establishes the Ledger as Layer 1: the immutable canonical log of events MOM has ingested. The Vault remains Layer 2: the projection of those events into a queryable form. Something has to read the Ledger and produce the Vault — and only that something is allowed to write to the Vault.

This ADR names that component the **Crier** and pins down its contract.

**Crier subscribes to the Ledger, not the bus.** This is the key architectural choice. The bus (`bus/herald`) is alive and useful for consumers that want at-most-once, in-process, ephemeral delivery — Drafter, Logbook, Lens hooks. But the Vault is durable state, and durable state must be derivable from durable input. Crier reads from `storage/ledger` by polling the next unapplied offset, applies the event, persists its new checkpoint, and loops. If the process restarts, Crier resumes from the checkpoint; if the Vault is deleted, Crier resumes from offset 0 and rebuilds it.

This means the bus is no longer on the durability path. A bus subscriber that misses an event misses an event; a Crier that misses an event eventually catches up because its input is the Ledger, not a transient publish.

**Crier writes the Vault only through Librarian.** Per ADR 0009 and the existing architecture, Librarian is the sole gate to Vault writes. Crier respects that. The package `events/crier` imports `storage/librarian` and never imports `storage/vault` directly. An archtest in `events/crier/crier_arch_test.go` enforces:

```go
archtest.AssertNoDirectImport(t, ".", "github.com/momhq/mom/storage/vault")
```

Crier calls Librarian's write APIs (`InsertMemory`, `PromoteDraft`, `UpdateTags`, etc., as they evolve through the milestone). Librarian remains responsible for substance-immutability enforcement (ADR 0011), graph-fluent tag normalisation (ADR 0010), and any future write-side invariants. Crier is a translator from canonical events into Librarian calls; Librarian is the gatekeeper of what the Vault looks like.

**Crier is the only projector.** Drafter, Logbook, and any other bus subscriber may continue to write through Librarian (Logbook already does, for operational telemetry; Drafter writes drafts under explicit-record). But for the *projection of canonical events into Vault state*, Crier is alone. Two consumers projecting the same event into the Vault would produce double-writes and racing state; one consumer is the invariant.

A subtlety: Drafter writes happen on the bus path today (a turn lands on the bus, Drafter decides to materialise it as a draft, Librarian persists). After v0.50, Drafter still subscribes to the bus and still writes drafts through Librarian — but the *canonical record* of the turn is in the Ledger and is projected by Crier independently. Drafter's draft is metadata about a turn, not the turn itself. The two flows do not collide because they write different rows (or, for shared rows, the schema gives Crier the substance and Drafter the operational fields per ADR 0011).

**Projection is deterministic and self-sufficient.** ADR 0018 requires every canonical event to be self-sufficient. Crier exploits this: each event is projected in isolation. There is no batching, no transactional grouping across events, no "look up the previous event to compute this one." A single canonical event arrives, Crier maps its `Type` to a projection function, calls Librarian with the resulting arguments, and advances its checkpoint. The replay test (Phase 3.4 in the v0.50 plan) takes any single Ledger event in isolation, runs it through Crier, and asserts the Vault delta matches a fixture.

This determinism is the foundation of recovery. "Rebuild the Vault" is "delete `mom.db`, run Crier from offset 0 to head." The operation is not magic; it is a finite sequence of projections each of which is testable in isolation.

**At-least-once semantics with idempotency.** Crier persists its checkpoint *after* the Librarian write succeeds. If the process crashes between the write and the checkpoint persist, the event is re-applied on next startup. Projections must therefore be idempotent: re-projecting `capture.turn.observed` for the same `(session_id, timestamp)` does not insert a duplicate row. Librarian's `InsertMemory` already uses UUIDs (ADR 0013); idempotency for events with no natural key (e.g. `operational.daemon.started`) is achieved by including the Ledger `offset` as a deduplication key on the projection side.

The alternative (checkpoint before write, exactly-once via two-phase commit) is not worth the complexity for a local SQLite-backed projection. At-least-once + idempotent projections is the standard pattern and the right one here.

**Crier and bus subscribers coexist.** When a canonical event is published, two things happen in sequence:

1. Editor (ADR 0020) appends the event to the Ledger and publishes to the bus.
2. Bus subscribers (Drafter, Logbook, Lens hooks) react immediately for low-latency UX.
3. Crier reads from the Ledger asynchronously and projects into the Vault.

Latency for Crier's projection is bounded by its polling interval (default: a tight loop with a short backoff when idle). For typical interactive use the lag between event-on-bus and event-in-Vault is sub-second. For correctness, however, the design treats Crier as the source of Vault state and the bus subscribers as effects.

**No bus reads for Crier.** A tempting shortcut is to have Crier double-subscribe (Ledger for replay, bus for live) so that it doesn't have to poll. Rejected: makes Crier care about two delivery semantics, complicates the "bus is not on the durability path" story, and the simpler design (Ledger only) is fast enough. If polling latency becomes a measured problem, we add a notification primitive between Editor's Ledger-append and Crier's loop — not a second subscription.

**Ordering.** Crier projects events in Ledger order. Out-of-order projection is not supported in v1 and not needed: the Ledger assigns monotonic offsets in append order, and the Editor appends in publish order. If a future event family needs partition-level parallelism, the partition key is added to the Ledger envelope and Crier learns to parallelise within partitions while serialising offset-advancement; this is a v0.60+ concern.

## Consequences

- The Vault is fully derivable from the Ledger. "Re-project the world" is a routine operation, not a recovery panic.
- The bus is no longer on the durability path. Bus crashes lose bus events but not Vault state.
- Drafter, Logbook, and Lens keep using the bus and Librarian as before. Their write paths are unchanged.
- Crier is the sole projector, enforced by archtest. Future drift ("just write to the Vault from this new package") is blocked at PR time.
- Projections must be idempotent. New canonical events ship with a projection function and an idempotency proof in the form of a replay test.
- A reset path exists: delete `mom.db`, restart, Crier rebuilds. This is documented in `mom doctor` as the recovery flow.
- Crier's checkpoint is operational state (per ADR 0011) — it lives somewhere mutable (likely in the Vault as a one-row `crier_state` table managed by Librarian). Resetting the checkpoint to 0 + dropping the Vault triggers a full reprojection.

## Considered alternatives

- **Crier reads from the bus.** Rejected: ties Vault durability to bus liveness. A bus crash mid-publish would lose the projection of the missed event; recovery would require replaying from the Ledger anyway. The shorter and more correct design is to read from the Ledger directly.
- **Multiple projectors (one per family, one per consumer).** Rejected: race conditions when two projectors touch the same Vault row, and the "Crier is sole projector" invariant becomes a coordination problem. Single projector + family-aware projection function is simpler and the Ledger is fast enough that one projector keeps up.
- **Crier writes the Vault directly (bypass Librarian).** Rejected: defeats the gatekeeping property Librarian provides (substance immutability, tag normalisation, future write invariants). Crier is a *user* of Librarian, not a peer.
- **Exactly-once semantics via two-phase commit.** Rejected for v1: complexity outweighs benefit for a local SQLite projection where re-applying an idempotent event is fast. At-least-once + idempotent is the right point on the curve.
- **Crier subscribes to the Ledger via a watch/notify primitive (no polling).** Considered. Deferred: polling with a backoff is sufficient and avoids introducing a notification API surface in the Ledger driver. If we measure latency that matters, we add notification then.
- **Reuse `workers/` for Crier (e.g. `workers/crier`).** Rejected: workers in the layout (Drafter, Logbook, Cartographer) are bus subscribers that produce side effects. Crier reads from the Ledger and writes to the Vault — a different role. Putting it under `events/crier` next to Editor and the registry keeps the projection pipeline visible as a unit.
- **Crier and Drafter merged into one package.** Rejected: they have different inputs (Ledger vs bus), different write semantics (durable projection vs operational metadata), and merging them couples two release cadences. Keeping them separate keeps each one's contract small.
- **Crier handles retention (delete-from-Vault).** Rejected: retention is the Gardener's job (Item 3, deferred to v0.60). Crier projects what happened; Gardener forgets what no longer needs to be remembered. Two policies, two components.
