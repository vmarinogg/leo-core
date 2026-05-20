# 0020 — Editor as canonicalization gateway

In the current architecture, ingress packages publish to the bus directly. The watcher's `ToPayload()` method rolls a `Turn` into a `map[string]any` and calls `bus.Publish(herald.Event{...})` in the same step (`cli/internal/watcher/watcher.go`). The CLI does the same for explicit records. The MCP server does the same for tool invocations that produce events. Each ingress surface owns its own translation step, and each consumer learns the union of all such translations by reading every producer.

ADR 0018 establishes that every event past the Ingress boundary must conform to a registered schema. ADR 0019 establishes the Schema Registry and chooses level B governance. This ADR names the package that performs the translation, fixes its position in the pipeline, and turns the position into an enforceable invariant.

**The Editor.** A new package `events/editor` exposes a single contract:

```go
func Canonicalize(raw any, src Source) (herald.Event, error)
```

`raw` is the adapter-shaped value the Ingress already has (a `Turn`, a CLI record payload, an MCP tool invocation). `src` declares which adapter is calling (informational; used for provenance stamping). The Editor returns a `herald.Event` whose `Type` matches a registered schema and whose `Payload` is the canonical shape that schema describes. The Editor calls `registry.Validate` internally; level-B violations attach the `_schema_violation` marker per ADR 0019 and still return a publishable event.

**Editor sits post-Ingress, pre-bus.** This is the architectural invariant. Every ingress surface — `ingress/cli`, `ingress/mcp`, `ingress/watcher/adapters/*` — calls `editor.Canonicalize` and publishes the result. No ingress surface calls `bus.Publish` directly with adapter-shaped data. No raw `Turn` (or future per-adapter struct) crosses the bus boundary.

**Why a single gateway.** Three forces converge:

1. *Consumers stop knowing about producers.* Drafter, Crier, Logbook, and future bus subscribers read canonical events. They don't import the watcher; they don't import the MCP server. Adding a fourth harness (e.g. OpenCode, per #155) adds a new adapter that calls the Editor, and the existing bus consumers learn nothing new.
2. *Schema validation has a single call site.* Level B governance (ADR 0019) only works if every event is validated. A single gateway is one call site to audit; N gateways is N call sites and an inevitable miss.
3. *Provenance stamping is centralized.* The Editor stamps `provenance_actor`, `provenance_source_type`, and `provenance_trigger_event` (ADR 0014) from a single rule set rather than letting each ingress surface invent its own values.

**Enforced via archtest.** A test in `events/editor/editor_arch_test.go` asserts:

- `bus/herald` does not import any `ingress/watcher/adapters/*` package, any `ingress/cli/internal-payload` type, or any other adapter-shaped type. The bus knows only `herald.Event`.
- Every package that imports `bus/herald` either *is* `events/editor` or does not import any `ingress/*` package. (i.e. you can subscribe to the bus or you can be the Editor; you can't be a non-Editor publisher that also touches Ingress types.)

The rules are slightly verbose to state but trivial to enforce — `shared/archtest.AssertNoDirectImport` (current package `cli/internal/archtest`) already supports the idiom (`archtest.go:76-88`). The archtest catches a future drift where someone adds a "quick" bypass and silently re-creates the multi-gateway problem.

**Stateless and synchronous.** The Editor holds no state beyond the registry it loaded at startup. `Canonicalize` is a pure function from `(raw, src)` to `herald.Event` (plus log side-effects). It does not buffer, batch, or schedule. The order of events on the bus is the order producers call the Editor.

**Editor and Ledger.** The Editor writes the canonical event to the Ledger (ADR 0021) **before** publishing to the bus. The order is fixed: canonicalize → validate → append to Ledger → publish to bus. If the Ledger append fails, the publish does not happen and the caller sees the error. The Ledger is the source of truth; the bus is a projection that can be rebuilt from the Ledger by Crier (ADR 0022).

**Provenance and project scoping.** The Editor is also the resolver site for `project_id` (per ADR 0016). The Source declares the cwd that was active when the event was produced (carried by each adapter from the relevant turn / CLI invocation), and the Editor walks up looking for `.mom-project.yaml`. Centralising this means there is one place that stamps `project_id`, one cache to invalidate when the file changes, and one resolution rule to test.

## Consequences

- Every consumer in `workers/`, `services/`, and `events/crier` is decoupled from every producer in `ingress/`.
- Adding an adapter is a self-contained change: write the adapter, hand its output to the Editor, done. No bus code is touched.
- Schema violations all flow through one chokepoint and become observable as a single metric.
- The Ledger-before-bus ordering means a crash between Ingress and bus loses *the bus event*, not *the canonical record*. On restart, the Ledger contains the event and Crier reprojects it.
- The Editor becomes a critical path: every event in the system passes through it. Its tests carry weight; an archtest enforces nobody silently goes around it.
- The current `Turn.ToPayload()` method is removed (or kept as a private helper inside `ingress/watcher`) once the Editor consumes the `Turn` directly. The transitional shim lasts one release; ADR 0023 retires it on the same cadence as MCP retirement.

## Considered alternatives

- **Per-adapter canonicalization (status quo).** Rejected: each adapter reinvents schema, provenance, and project resolution. The drift between adapters is the bug ADR 0018 exists to fix.
- **Canonicalization as a bus-side filter (publish raw, transform on read).** Rejected: bus subscribers would each implement the transform, and the raw shape would leak into Logbook, Drafter, and Crier. The single-gateway argument applies in the other direction: one place to translate beats N places.
- **Canonicalization in the registry (`registry.Canonicalize(raw)`).** Rejected: blurs the registry's role (data + validation) with a runtime adapter mapping. The registry says what a schema *is*; the Editor says how an adapter's input *maps to* a schema.
- **Async Editor (buffers + worker pool).** Rejected for v1: hides ordering bugs behind a queue, complicates Ledger ordering, and the current load profile does not warrant the complexity.
- **Multiple Editors (one per adapter, sharing the registry).** Rejected: just gives the same problem a different filename. The point of a gateway is that there is one.
- **Editor as a method on each adapter type (`adapter.Editor`).** Rejected: ties the Editor to adapter packages and prevents the archtest invariants. Free function with explicit `Source` argument is cleaner.
- **Validate-only Editor (publish whatever the producer hands in, validate after).** Rejected: produces events on the bus that the registry just declared invalid. Validation is part of the gate, not a side-effect downstream of it.
- **Skip the Editor; have Crier validate at projection time.** Rejected: pushes the schema contract from "everything on the bus is canonical" to "Crier hopes the bus is canonical." Drafter, Logbook, and any future bus consumer would each need their own validation step. Editor centralises it.
