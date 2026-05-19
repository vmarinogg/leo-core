---
name: mom-recall
description: Search MOM's persistent memory. Use when user asks what was decided, discussed, preferred, tried, learned, or remembered about a specific topic.
user-invocable: true
allowed-tools: Bash(mom recall*), Bash(command -v mom*), Bash(brew install momhq/tap/mom*)
argument-hint: <query>
---

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

## Run

Ask the user for a natural-language query if one was not provided. Then run:

```bash
mom recall "<query>"
```

Behavior:

- If the user asked to show, find, or list memories, print the recalled items.
- If the user asked a question, answer it using only the recalled items.
- If nothing is returned, say no matching memories were found.

Output format when there are matches:

```text
Recalled <N> memories:

<direct answer in 2–6 lines>

Sources:
- memoryId: <id-1>
- memoryId: <id-2>
```

Rules:

- Never run `mom recall` without a query.
- Do not pass extra flags.
- Do not invent content beyond what `mom recall` returned.

## Postflight (version hint)

Any `mom ...` command may print a banner to stderr like:

```
MOM 0.40.1 available. Run `brew upgrade mom` or `mom self-update`
```

If you see that line, finish the task first, then add one short line at the end of your reply suggesting the upgrade. Do not run the upgrade yourself.
