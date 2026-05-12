# Contributing to MOM

## Prerequisites

- Go 1.22+
- make

## Setup

```bash
git clone https://github.com/momhq/mom.git
cd mom/cli
make build
make test
```

## Project structure

```
cli/
├── cmd/mom/main.go              # entrypoint
├── internal/
│   ├── cmd/                     # cobra commands (init, upgrade, CRUD, ops, export)
│   ├── adapters/runtime/        # RuntimeAdapter interface + impls (claude, codex, windsurf)
│   ├── adapters/storage/        # StorageAdapter interface + impls (JSON)
│   ├── config/                  # .mom/config.yaml handling
│   ├── memory/                  # memory document types and validation
│   ├── mcp/                     # MCP server (tools + resources)
│   ├── cartographer/            # multi-repo scope detection
│   ├── gardener/                # memory lifecycle, dedup, landmarks
│   ├── transponder/             # local telemetry emitter (deprecated, see Herald)
│   └── scope/                   # scope resolution
├── Makefile
├── go.mod
└── go.sum

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
cd cli && go test ./internal/cmd/ -run 'TestGuardrail_'
```

Adding a new exception requires updating the corresponding allowlist in
`internal/cmd/architecture_guardrails_test.go` with explicit rationale.

## PR process

1. Fork the repo
2. Create a feature branch from `main`
3. Implement with tests (TDD)
4. Run `make test` and `make lint`
5. Submit a PR linking the related issue


## License

By contributing, you agree that your contributions will be licensed under the [Apache 2.0 License](LICENSE).
