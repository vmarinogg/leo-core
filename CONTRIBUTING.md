# Contributing to MOM

## Prerequisites

- Go 1.22+
- make

## Setup

```bash
git clone https://github.com/momhq/mom.git
cd mom
make build
make test
```

## Project structure

The codebase uses a role-based top-level layout (see [ADR 0017](adr/0017-role-based-repo-layout.md)). Each bucket maps 1:1 to the role a package plays in the dataflow — not to whether it reads or writes.

```
cmd/mom/main.go                  # entrypoint
ingress/                         # external input → in-process events
├── cli/                         # cobra subcommands (init, recall, record, ops, …)
├── mcp/                         # MCP stdio server (deprecated in v0.50 per ADR 0023)
├── watcher/                     # harness transcript watchers (Claude, Codex, Pi)
├── harness/                     # harness capability registry (detection, tiers)
└── record/                      # shared explicit-write code path
events/                          # canonical event pipeline (v0.50 work)
├── editor/                      # canonicalization gateway (post-Ingress, pre-bus)
├── registry/                    # schema registry (governance level B, ADR 0019)
└── crier/                       # projector/replayer (Ledger → Vault via Librarian)
bus/herald/                      # in-process event bus
workers/                         # bus subscribers with side effects
├── drafter/                     # event-stream → draft memories
├── logbook/                     # operational telemetry
├── cartographer/                # bootstrap seeding (parked, #240)
└── gardener/                    # landmark computation; retention planned for v0.60
services/                        # read-side application code
├── finder/                      # recall query planner (FTS5 + escalation)
└── lens/                        # local dashboard HTTP server
storage/                         # durable state
├── vault/                       # SQLite primitive
├── librarian/                   # sole gate to vault writes/reads (ADR 0009)
├── canonical/                   # canonical-path resolution + migration aggregation
├── memory/                      # memory document types
└── legacy/                      # pre-v0.30 JSON storage (migration path only)
ops/                             # background lifecycle
├── daemon/                      # platform service management
└── diagnose/                    # introspection
shared/                          # cross-cutting utilities
├── config/                      # config.yaml (harness + watcher settings)
├── pathutil/                    # path canonicalization
├── scope/                       # multi-level discovery
├── project/                     # .mom-project.yaml resolution (ADR 0016)
├── ux/                          # TUI/output helpers
└── archtest/                    # architectural invariant tests
Makefile
go.mod
go.sum

.mom/                            # MOM's own memory (dogfooding)
├── config.yaml                  # preferences
├── identity.json                # project identity
├── memory/                      # memory documents (JSON)
├── constraints/                 # always-active guardrails
├── skills/                      # composable procedures
├── schema.json                  # document schema
└── index.json                   # tag-based index
```

See [.github/repo-surface.md](.github/repo-surface.md) for the full one-line
justification of every tracked top-level item and the rules for adding new ones.

## Adding a runtime adapter

1. Create a new file in `internal/adapters/runtime/` (e.g. `cursor.go`)
2. Implement the `Adapter` interface defined in `runtime.go`
3. Add tests in a `_test.go` file (TDD: tests first)
4. Register the adapter in the `init` command

Use the `ClaudeAdapter` as reference.

## Commit conventions

We use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` new feature
- `fix:` bug fix
- `docs:` documentation
- `test:` tests
- `refactor:` code restructuring

## Code style

Follow patterns from [go-patterns](https://github.com/tmrts/go-patterns). Key principles:

- Strategy pattern for adapters
- Factory functions (`New...`) for constructors
- Interfaces accepted, structs returned
- Table-driven tests

## TDD

All code must follow test-driven development:

1. Write tests first
2. Verify they fail
3. Implement
4. Verify they pass

## Architecture guardrails

The CLI package ships a small set of guardrail tests that enforce
post-alpha main-flow invariants — public CLI surface, harness terminology,
and core-flow architecture. They run as part of the normal Go test suite
and in CI. To run them in isolation:

```bash
go test ./ingress/cli/ -run 'TestGuardrail_'
```

Adding a new exception requires updating the corresponding allowlist in
`ingress/cli/architecture_guardrails_test.go` with explicit rationale.

## PR process

1. Fork the repo
2. Create a feature branch from `main`
3. Implement with tests (TDD)
4. Run `make test` and `make lint`
5. Submit a PR linking the related issue


## License

By contributing, you agree that your contributions will be licensed under the [Apache 2.0 License](LICENSE).
