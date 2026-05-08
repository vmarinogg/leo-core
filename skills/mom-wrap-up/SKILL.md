---
name: mom-wrap-up
description: Curate recent MOM drafts. Use when user asks to wrap up, finish, close the session, preserve decisions, or prepare memory before clearing context.
user-invocable: true
allowed-tools: Bash(mom drafts*), Bash(mom curate*)
---

Run only after explicit user request.

1. Surface recent drafts:

```bash
mom drafts
```

If user gives a Go duration window, use it:

```bash
mom drafts --since 1h
```

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
