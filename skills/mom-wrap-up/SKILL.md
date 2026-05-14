---
name: mom-wrap-up
description: Curate recent MOM drafts. Use when user asks to wrap up, finish, close the session, preserve decisions, or prepare memory before clearing context.
user-invocable: true
allowed-tools: Bash(mom drafts*), Bash(mom curate*)
---

Run only after explicit user request.

1. Surface recent drafts. By default `mom drafts` is scoped to the project that owns the current working directory (per ADR 0016 — the `.mom-project.yaml` binding) — concurrent sessions in other projects do not leak into the list.

```bash
mom drafts
```

If user gives a Go duration window, use it:

```bash
mom drafts --since 1h
```

Narrow further if context calls for it:
- `--harness codex` — only drafts from a specific harness (claude-code, codex, pi)
- `--session <id>` — only drafts from one session, when the user knows the id
- `--all-projects` — disable the cwd scope (cross-project wrap-up)
- `--strict-project` — exclude legacy drafts with no `project_id`

The output columns are `ID  Created  Harness  Project  Summary`. The Harness and Project columns help the agent and user tell concurrent-session drafts apart at a glance.

2. Synthesize a curation plan.

For each draft worth keeping, propose:
- draft id
- type: `semantic`, `procedural`, or `episodic`
- approved summary
- reason to curate

Hide drafts you recommend discarding unless user asks to see them.

3. Wait for user approval.

Do not curate anything before approval.

4. Execute approved curation exactly:

```bash
mom curate <id> --type <semantic|procedural|episodic> --summary "<approved summary>"
```

5. Report:

```text
## Wrap-up complete
Curated:  <N>
Deferred: <N or none>
```

Do not:
- Use MCP.
- Use ad hoc database queries.
- Use removed legacy curation commands.
- Rewrite draft content.
- Skip type or summary.
