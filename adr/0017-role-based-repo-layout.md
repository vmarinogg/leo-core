# 0017 — Role-based repo layout

MOM's `cli/internal/` directory has grown organically since v0.13: 32 sibling packages with no visible architecture. `watcher/`, `herald/`, `drafter/`, `librarian/`, `vault/`, `mcp/`, `daemon/` and twenty-five others sit side by side at the same depth. Readers cannot tell at a glance which packages are ingress, which are storage, which are workers, and which are shared infrastructure. New contributors discover the architecture only by reading code paths end-to-end. Architectural rules (e.g. "the read path must not depend on the Ledger", "Vault writes must route through Librarian") have no structural representation — they live in archtests and reviewer memory.

This ADR adopts a role-based top-level layout that names the architecture in the filesystem. The eight buckets correspond 1:1 to the roles a package plays in the MOM dataflow, not to whether it reads or writes:

```
mom/
├── cmd/mom/                              entrypoint
├── ingress/
│   ├── cli/                              CLI subcommands (user-driven ingress)
│   ├── mcp/                              MCP server (harness ingress, deprecated)
│   └── watcher/adapters/{claude,codex,pi}/   harness transcript ingress
├── events/
│   ├── editor/                           canonicalization gateway (post-Ingress, pre-bus)
│   ├── registry/schemas/                 schema registry — source of truth
│   └── crier/                            projector/replayer (Ledger → Vault)
├── bus/herald/                           in-process event bus
├── workers/
│   ├── drafter/                          memory drafting from events
│   ├── logbook/                          operational telemetry
│   └── cartographer/                     bootstrap seeding (parked)
├── services/
│   ├── finder/                           recall query plan
│   └── lens/                             session dashboard
├── storage/
│   ├── librarian/                        sole gate to Vault writes/reads
│   ├── vault/                            SQLite-backed memory store
│   └── ledger/                           append-only canonical event log
├── ops/
│   ├── daemon/                           background process lifecycle
│   └── diagnose/                         doctor + introspection commands
├── shared/
│   ├── config/                           configuration loading
│   ├── pathutil/                         path canonicalization
│   ├── scope/                            project resolution
│   ├── ux/                               TUI/output helpers
│   └── archtest/                         architectural invariants
└── docs/
```

Role beats read/write axis. Splitting `readers/` from `writers/` would put Finder (read) and Crier (write) in different buckets even though both project events into the Vault contract — and would put Librarian (both) somewhere awkward. Role naming keeps each package next to its dataflow neighbours: events live with events, storage lives with storage, and reviewers know where a new package belongs without litigating its read/write profile.

`ingress/` includes both user-driven surfaces (CLI subcommands, MCP server) and machine-driven surfaces (watcher adapters). They share a role — translating external input into in-process events — and benefit from sitting together. `services/` covers the read-side application code (Finder, Lens) that composes storage primitives into user-facing answers. `workers/` covers consumers that subscribe to the bus and produce side effects other than Vault projection (Drafter, Logbook, Cartographer). `events/` is the new bucket created by this milestone: Editor canonicalizes raw input, the registry stores schemas, and Crier replays canonical events into the Vault. The `bus/` bucket holds the existing `herald` event bus and any future transports.

`shared/` is reserved for genuinely cross-cutting utilities (path handling, configuration, scope resolution, UX helpers, archtest). It is not a dumping ground; anything that names a role belongs in that role's bucket.

The Go module path remains `github.com/momhq/mom`. Internal import paths change from `github.com/momhq/mom/cli/internal/<pkg>` to `github.com/momhq/mom/<bucket>/<pkg>`. The reorganization is mechanical: ~94 Go files import ~32 distinct `cli/internal/*` packages, and a single PR rewrites every import in lockstep with the directory moves. `cli/internal/` ceases to exist; an archtest enforces that no future package re-introduces it.

The decision is made now, in v0.50.0, because Items 1 (canonical events) and 2 (Ledger + Crier) introduce three new packages (`events/editor`, `events/crier`, `storage/ledger`). Landing the layout first means those packages drop into their final homes from day one and no second migration is required.

## Consequences

- Every architectural invariant in the roadmap maps to a concrete import-path rule: `archtest` rules (e.g. "`services/*` must not import `storage/ledger`", "`bus/herald` must not import raw adapter types") become readable in a glance because both sides of the rule are named by their role.
- The reorganization is a single big-bang PR. Long-lived rename branches generate constant conflicts; one merge window with an empty queue is cheaper than a multi-week migration.
- New contributors orient by reading the top-level directory listing. Onboarding documentation can point at the layout and stop.
- The MCP server moves under `ingress/mcp` rather than disappearing immediately — ADR 0023 retires the transport in v0.60+, not in v0.50.
- `mom-engineer/` and other internal-only documentation directories stay outside this repository. The layout describes what ships, not what supports the team.

## Considered alternatives

- **Keep `cli/internal/*` as a flat directory.** Rejected: the architecture exists; refusing to name it in the filesystem just makes every new contributor rediscover it. The layout is the cheapest possible documentation of the system.
- **Split readers and writers (`readers/` vs `writers/`).** Rejected: collapses meaningfully different roles. Finder (reads Vault to answer queries) and Crier (writes Vault from Ledger events) play very different parts in the system; lumping both into `writers/` because Crier writes, or both into `readers/` because Finder reads, hides the distinction the architecture is built on.
- **Layered architecture (`presentation/`, `application/`, `domain/`, `infrastructure/`).** Rejected: classical layering implies a strict dependency direction that MOM's pipeline doesn't follow. The bus is not above or below storage; it's adjacent. Forcing a layer name on each bucket would invite incorrect dependency assumptions.
- **Feature folders (`recall/`, `record/`, `lifecycle/`).** Rejected: a single feature (recall) touches CLI, services, storage, and shared utilities. Co-locating by feature scatters the same role across many buckets and defeats the architectural-invariant story.
- **Module split (multiple Go modules).** Rejected for v1: the codebase is small enough that a single module keeps tooling simple and refactors cheap. A future split (e.g. `cli/`, `events/`, `storage/` as separate modules) remains open if the team grows or external consumers want to depend on subsets.
- **Keep `mcp/` at the top level rather than under `ingress/`.** Rejected: MCP is one of several ingress surfaces; it is not architecturally distinct from CLI ingress or watcher ingress. Promoting it to a top-level bucket would imply special status it doesn't have, especially given the v0.60+ retirement plan in ADR 0023.
- **Defer the reorganization to v0.60.** Rejected: Items 1 and 2 add three new packages this milestone. Adding them under `cli/internal/` and moving them in v0.60 means doing the import rewrite twice. Cheaper to land the layout once, now.
