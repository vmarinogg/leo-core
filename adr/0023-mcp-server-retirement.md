# 0023 — MCP Server retirement (deprecated in v0.50, retired in v0.60+)

When MOM first shipped, the MCP server was the canonical way for an LLM harness to invoke MOM operations: a long-lived process speaking the Model Context Protocol, exposing `mom_status`, `mom_recall`, `mom_get`, `mom_landmarks`, and `mom_record` as tools the harness could call. Two forces have since shifted the equation:

1. **The CLI grew up.** `mom status`, `mom recall`, `mom record` and friends are no longer thin wrappers around MCP handlers — they are independent code paths that read and write the central vault directly. Where the MCP server existed because there was no other way to reach MOM from a harness, the CLI now offers the same surface as ordinary subprocesses.
2. **Harness ergonomics improved.** Claude Code, Codex, and Pi all support invoking subprocesses (Bash tool, shell hook) cleanly. The MCP transport solved a problem (long-lived state, structured tools) that the harnesses now solve themselves; one less long-lived server is one less thing to start, monitor, and authenticate.

This ADR retires the MCP server. The retirement is staged: **v0.50 deprecates with a runtime warning; v0.60+ removes the transport.** This ADR documents both the end state and the bridge.

**End state (v0.60+).**

- The MCP server binary (`mom serve mcp`) is removed.
- Harnesses invoke `mom <subcommand>` as subprocesses for every operation they previously called via MCP tools.
- The harness adapter packages (`ingress/watcher/adapters/*`) continue to ingest transcripts; this is a separate concern and is unaffected. Watcher-driven *capture* stays; MCP-driven *invocation* leaves.
- The `mom_status` semantics are preserved through `/mom-status` (skill) + `mom status` (CLI). The instructions block currently returned by the MCP `instructions` field is delivered through MOM's existing global-agent-file bootstrap (ADR 0015), which all supported harnesses already concatenate into the agent's context.

**v0.50 deprecation behaviour.**

- The MCP server is unchanged at the protocol level: it still exposes the same five tools and serves the same responses.
- On startup, the server emits exactly one deprecation warning to stderr:
  ```
  WARN: MOM's MCP transport is deprecated and will be removed in v0.60+.
        Configure your harness to invoke `mom <subcommand>` as a subprocess.
        See adr/0023-mcp-server-retirement.md for details.
  ```
- A unit test asserts the warning is emitted exactly once per server boot. The check is on the boot path, not the request path; existing MCP sessions stay quiet after the initial line.
- The `mom_status` MCP handler additionally returns the deprecation message inside its JSON payload (a `deprecation` field) so harnesses that log the structured response surface the warning to users.

**CLI parity audit.** Before v0.50 tags, every MCP tool must have a functionally equivalent CLI subcommand. The current state (verified against `cli/internal/cmd/root.go` and `cli/internal/mcp/tools.go`):

| MCP tool | CLI counterpart | Gap |
|---|---|---|
| `mom_status` | `mom status` (`cli/internal/cmd/ops.go`) | — |
| `mom_recall` | `mom recall <query>` (`cli/internal/cmd/recall.go`) | — |
| `mom_record` | `mom record` (stdin-piped, `cli/internal/cmd/record.go`) | — |
| `mom_get` | **missing** | Add `mom get <id>` subcommand |
| `mom_landmarks` | **missing** | Add `mom landmarks [--limit N]` subcommand |

The two gaps ship as part of the v0.50 milestone (Phase 3 of the execution plan, sub-PR (a)). The new subcommands read through Finder/Librarian — the same code paths the MCP handlers use today — and produce JSON output compatible with what the MCP tools return, so a harness migrating from `mom_get` to `mom get --json <id>` sees the same shape.

**"Functionally equivalent" means.** The CLI subcommand:

1. Reads or writes the same data as its MCP counterpart, using the same underlying packages.
2. Accepts the same logical inputs (with CLI-shaped argument forms — flags or positional args rather than JSON properties).
3. Produces JSON output structurally equivalent to the MCP tool's response when invoked with `--json` (default for non-TTY stdout, opt-in for TTY).
4. Exits 0 on success and non-zero with a structured error on failure.

The parity is enforced by a test in `ingress/mcp/parity_test.go` that, for each MCP tool, calls both the MCP handler and the CLI subcommand against a fixture vault and asserts the responses match modulo well-known transport differences (e.g. JSON-RPC framing).

**The `instructions` story.** The MCP `instructions` field today is correct per the protocol spec but not actually injected by any supported harness (per ADR 0015). MOM's operating protocol reaches agents through the global-agent-file bootstrap blocks (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, etc.), and that delivery path does not depend on the MCP server. Removing MCP does not remove agent-side instructions; the bootstrap blocks already handle it.

**The `mom_status` discovery story.** Today the MCP server is what agents call to "find MOM" at session start. In the CLI-subprocess model, the agent runs `mom status` (or the `/mom-status` skill) instead. The global-agent-file bootstrap block already instructs the agent to do this; ADR 0015 wrote the bootstrap to be transport-agnostic for exactly this reason.

**Migration window.** v0.50.x runs the MCP server with the deprecation warning. The CHANGELOG announces the v0.60 removal. Anyone running MOM through MCP gets the warning every session boot for the duration of the v0.50 cycle.

**What is not retired in v0.50.** The MCP package itself stays compiled and shipped in v0.50. Removing it is a v0.60 follow-up issue (referenced from this ADR by GitHub URL in the relevant issue body). v0.50's job is to (a) confirm CLI parity, (b) install the warning, (c) close the parity gaps. v0.60's job is to delete the server.

## Consequences

- v0.50 is non-breaking for anyone using the MCP transport: it still works, with a noticeable warning. Users have a full release cycle to switch harness configurations.
- The CLI gains two new subcommands (`mom get`, `mom landmarks`) which close the parity gaps and become the public interface for those operations.
- A `parity_test` in `ingress/mcp/` prevents the CLI and MCP outputs from drifting during the deprecation window.
- Once v0.60 removes MCP, MOM ships a smaller binary and one fewer long-lived process. No daemon supervision is needed for invocation paths; the watcher daemon (separate concern) remains for transcript capture.
- Documentation referencing `mom serve mcp` is updated in v0.50 to call it "deprecated" and to point at the CLI-subprocess pattern. Removed entirely in v0.60.
- The harness-side change is "swap the MCP tool definition for a shell-out to `mom <cmd>`." MOM's docs ship a small migration guide for each supported harness.

## Considered alternatives

- **Retire MCP in v0.50 (hard cut, no deprecation cycle).** Rejected: breaks anyone running through MCP without warning. A one-release deprecation is cheap and gives users a real window.
- **Keep MCP indefinitely as a parallel surface.** Rejected: two surfaces means two test surfaces, two failure modes, and two answers when a user asks "how do I invoke MOM?" The CLI is winning; the second surface is overhead.
- **Replace MCP with a different long-lived protocol (e.g. gRPC, JSON-RPC over a socket).** Rejected: solves a problem we don't have. The CLI is fast enough; subprocess startup is not a measured bottleneck; and "go back to a long-lived server" would re-introduce the daemonisation overhead that motivated this retirement in the first place.
- **Move MCP tools onto the watcher daemon (one process, two transports).** Rejected: conflates capture (transcript ingestion) with invocation (agent-initiated tool calls). The daemon's reason to exist is the watcher; MCP doesn't need to be there.
- **Retire MCP without a parity audit (assume the CLI is close enough).** Rejected: the audit caught two real gaps (`mom_get`, `mom_landmarks`). Closing them is part of the milestone, not a follow-up.
- **Convert MCP into a thin shim that itself invokes CLI subprocesses.** Rejected for v0.50: would have to live somewhere, would still need maintenance, and the gain (smaller MCP code) is marginal compared to "just call the CLI from the harness." If a future user has an unusual transport need, the door is not closed — they can write the shim themselves.
- **Keep the `instructions` field as the canonical bootstrap channel.** Rejected: ADR 0015 already chose the global-agent-file pattern because no supported harness injects MCP `instructions` into agent context. Doubling down on `instructions` would invest in a path no harness uses today.
- **Defer the CLI parity work to v0.60.** Rejected: the deprecation warning is meaningless if users discover, after migrating their harness, that `mom_get` has no CLI counterpart. Parity ships with the warning.
