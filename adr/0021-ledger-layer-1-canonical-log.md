# 0021 — Ledger as Layer 1 immutable canonical log

Until v0.50, the Vault is MOM's only durable store. Memories, drafts, operational events, and filter audits all land in the same SQLite file (`$HOME/.mom/mom.db`, per ADR 0009). The Vault is excellent at what it does — graph-fluent recall, FTS, joins (ADR 0010) — but it is also a *projection*. Tag normalisation, draft promotion, type assignment, and other lifecycle operations rewrite Vault state. The system has no record of what arrived, only of what survived all the post-processing.

This ADR introduces a second storage layer beneath the Vault: the **Ledger**. The Ledger holds canonical events (ADR 0018), validated by the registry (ADR 0019), canonicalized by the Editor (ADR 0020). It is append-only, immutable, and independent of the Vault. The Vault becomes Layer 2 — a projection of the Ledger maintained by Crier (ADR 0022).

**Storage location.** The Ledger lives at `$HOME/.mom/ledger/` — a directory under the same home dir as the Vault, but **not** inside `mom.db`. The Ledger is its own file/directory tree, not a Vault table. Two physical artefacts, two responsibilities, two backup units.

The choice not to add a `ledger_events` table to `mom.db` is deliberate. Sharing a SQLite file would couple Ledger writes to Vault lock contention, would entangle backup semantics (you can't restore the Vault without restoring the Ledger and vice versa), and would invite operational shortcuts like "just SELECT from the Ledger table in this Finder query" that this ADR exists to forbid.

**File format.** The Ledger is a directory of segment files. Each segment is an append-only file of length-prefixed JSON event records. A `current` symlink (or a `current.txt` marker file on platforms without symlinks) points at the active segment. Segments rotate on size (default 64 MiB) or on day boundary, whichever comes first. The first 8 bytes of every segment are a magic number + version so older readers fail loudly on a future format change.

Each event in the Ledger carries:

- `id` — UUID assigned at append time (independent from `memory.id`).
- `offset` — monotonically-increasing 64-bit position, unique per Ledger.
- `appended_at` — server-clock timestamp at append time.
- `event` — the canonical `herald.Event` produced by the Editor (`Type`, `SessionID`, `Timestamp`, `Payload`).

`offset` is the durable identifier consumers use to checkpoint. Crier persists its last-applied offset; on restart it resumes from `offset + 1`.

**Append-only.** The Ledger driver opens segment files with `O_APPEND` and never with any other write flag. The driver exposes only `Append(event) (offset, error)`, `Read(offset) (event, error)`, and `Iterate(from offset) iter`. There is no `Update`, no `Delete`, no `Truncate` (except `Compact` — see below). An archtest scans the driver source for forbidden write modes; the same archtest forbids any caller from importing `os.O_TRUNC | O_APPEND` style flags in the Ledger package. Operational mutability (per ADR 0011) does not apply to the Ledger; the Ledger is *substance* in the literal sense.

**No mutation, ever — including for retention.** Memory retention (Item 3, deferred to v0.60) operates on the Vault projection, not on the Ledger. When a memory is deleted from the Vault, the originating Ledger event remains. Retention is a *forgetting* policy on Layer 2; the Layer 1 record of what was captured does not change. This separation is the whole reason for two layers: the historical truth and the operational projection are different things and deserve different rules.

**Compaction.** The single exception to append-only is *segment-level* compaction: closed segments older than a configurable horizon (default: never) may be discarded as whole files. Compaction is a coarse-grained operational tool, not a record-level edit. Even when compaction runs, Crier's checkpoint is preserved and the Ledger reports the new oldest-available offset. Within any unsealed segment no record is ever rewritten.

**Crash safety.** Every append is followed by an `fsync` on the segment file before the offset is returned to the caller. The Editor (ADR 0020) waits for the offset before publishing to the bus. If the process crashes between append and publish, the next startup sees the event in the Ledger and Crier reprojects it; if the process crashes during append, the partial write is detected on next open (length-prefix vs file-tail mismatch) and truncated to the last good record.

**No reads on the recall path.** The user-facing read path is and remains CLI → Finder/Librarian → Vault (ADR 0009). The Ledger is not a query target. Recall, landmarks, drafts, lens dashboards — all read from the Vault. The Ledger is read by exactly one consumer: Crier. Archtests in `services/finder` and `services/lens` forbid importing `storage/ledger`.

**Backup story.** Users back up `$HOME/.mom/` and get both layers in one tree. Sync (when it lands) ships Ledger segments through whatever transport makes sense; segments are immutable so they're trivially shareable. The Vault is a projection — losing it and reprojecting from the Ledger is a recovery path, not a crisis.

**What the Ledger is not.** It is not an audit log of API calls. It is not a debug trace of internal function calls. It is not a Kafka-like distributed log. It is one local append-only file tree that holds the canonical events MOM has ingested, sufficient to rebuild the Vault projection.

## Consequences

- MOM gains an independent historical record. Recovering from a corrupted Vault is "delete `mom.db`, re-run Crier from offset 0."
- Append is on the hot path of every captured turn. The cost is one `O_APPEND` write + `fsync`; on local disk this is sub-millisecond and acceptable.
- Two storage units live under `$HOME/.mom/`. Documentation, backup guides, and `mom doctor` all gain a second checklist item.
- Retention work (v0.60+, Item 3 deferred) plans against the Vault, not against the Ledger. The Ledger keeps growing until the user runs compaction on closed segments.
- The Ledger format is owned by MOM and not exposed as a public protocol. Format evolution (segment header, record framing) is internal and migrations ship in `mom upgrade`.
- An archtest enforces driver-level append-only semantics. Tampering with that requires changing the test, which is visible in code review.
- Crier (ADR 0022) is the only Ledger consumer in the codebase. The contract is narrow and testable.

## Considered alternatives

- **Add a `ledger_events` table to `mom.db`.** Rejected: couples Ledger writes to Vault lock contention, entangles backups, and invites direct SQL queries that defeat the Layer-1 / Layer-2 separation. Two layers in two files is the cheap way to keep the responsibilities clean.
- **Use an existing embedded log library (Bolt, Badger, LMDB, embedded Kafka).** Rejected for v1: bigger dependency, more knobs, fewer guarantees about the file layout being readable from outside Go. A directory of length-prefixed JSON segments is debuggable with `cat` and `jq`.
- **Use SQLite for the Ledger but in a separate file (`ledger.db`).** Considered seriously. Rejected because (a) SQLite invites mutability primitives that the archtest then has to forbid by convention rather than by file format; (b) the operational model (immutable segments, day rotation, compaction) maps poorly to a single-file database; (c) tooling (`cat`, `jq`, `tail -f`) on a segment directory is friendlier for debugging.
- **Write events to the bus first, then asynchronously to the Ledger.** Rejected: produces a window where the bus has events the Ledger does not. Crash recovery becomes "reconstruct what the bus thought it knew," which is impossible. Ledger-first is the only ordering that makes recovery deterministic.
- **Make Crier optional (Ledger writes, Vault writes happen directly elsewhere).** Rejected: defeats the projection model. The whole point of Layer 1 is that Layer 2 is derivable from it.
- **Allow Ledger compaction at record granularity (e.g. drop events older than 90 days).** Rejected: turns Layer 1 into another projection. Coarse-grained segment compaction is sufficient and preserves the immutability property within any open record range.
- **Store the Ledger in `$XDG_DATA_HOME` instead of `$HOME/.mom/`.** Rejected: ADR 0009 chose `$HOME/.mom/` for the Vault; the Ledger lives alongside it for consistency and one-tree backups.
- **Multiple Ledgers (one per project).** Rejected: ADR 0009 made the Vault central; per-project Ledgers would re-introduce the scatter problem (ADR 0016 makes project scoping a column, not a directory). The Ledger is global; events carry `project_id` per the Editor's resolution rule.
- **Write the Ledger record as the same shape as the bus event (no envelope).** Rejected: the Ledger needs `offset` and `appended_at`, which are Ledger-assigned, not Editor-assigned. The envelope (`{id, offset, appended_at, event}`) carries the durable identifiers; the inner `event` is byte-identical to what crossed the bus.
- **Drop the `fsync` for performance.** Rejected: the durability promise is the whole point. If `fsync` becomes a measured bottleneck, batching (group commit) is the next step, not skipping the flush.
