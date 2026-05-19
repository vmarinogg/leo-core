---
name: mom-wrap-up
description: Curate recent MOM drafts. Use when user asks to wrap up, finish, close the session, preserve decisions, or prepare memory before clearing context.
user-invocable: true
allowed-tools: Bash(mom drafts*), Bash(mom curate*), Bash(command -v mom*), Bash(brew install momhq/tap/mom*)
---

Run only after the user explicitly asks to wrap up.

## Preflight

Check that `mom` is on PATH:

```bash
command -v mom
```

If it is missing, tell the user MOM is not installed and ask permission to install it:

```text
MOM is not installed. Install it now with Homebrew?
  brew install momhq/tap/mom
Source: https://github.com/momhq/mom
```

If the user agrees, run that command. If the user declines, stop. Do not install MOM without explicit permission.

## Flow

1. List recent drafts scoped to the current project. `--strict-project` is **required** on every invocation, no exceptions:

   ```bash
   mom drafts --strict-project
   ```

   If the user gives a time window (Go duration like `1h`, `30m`, `2d`), pass it as well:

   ```bash
   mom drafts --strict-project --since 1h
   ```

   If the command returns zero drafts, report "no drafts in this project for the requested window" and stop. Do **not** retry without `--strict-project`. Do **not** widen the search to other projects on your own.

   Only add `--all-projects` if the user explicitly asks for a cross-project wrap-up. In that case, replace `--strict-project` with `--all-projects` (never run without one of the two).

   Other optional narrowing flags, only when context calls for it:
   - `--harness <name>` — restrict to one harness (`claude-code`, `codex`, `pi`)
   - `--session <id>` — restrict to one session when the user knows the id

   The output has columns `ID  Created  Harness  Project  Summary`.

2. Propose a curation plan. For each draft worth keeping, list:
   - draft id
   - type: `semantic`, `procedural`, or `episodic`
   - approved summary
   - one-line reason to keep it

   Hide drafts you recommend discarding unless the user asks to see them.

3. Wait for the user to approve. Do not curate anything before approval.

4. For each approved draft, run:

   ```bash
   mom curate <id> --type <semantic|procedural|episodic> --summary "<approved summary>"
   ```

5. Report when done:

   ```text
   ## Wrap-up complete
   Curated:  <N>
   Deferred: <N or none>
   ```

## Rules

- Always include `--strict-project` on every `mom drafts` call. Zero results means "no drafts in this project" — it does **not** mean "try a wider search". Only the user can opt into `--all-projects`.
- Never curate without user approval.
- Never skip `--type` or `--summary`.
- Do not rewrite draft content.
- Do not use MCP or run ad hoc database queries — only the commands above.

## Postflight (version hint)

Any `mom ...` command may print a banner to stderr like:

```
MOM 0.40.1 available. Run `brew upgrade mom` or `mom self-update`
```

If you see that line, finish the task first, then add one short line at the end of your reply suggesting the upgrade. Do not run the upgrade yourself.
