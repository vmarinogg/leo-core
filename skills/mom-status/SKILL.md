---
name: mom-status
description: Show MOM's current state. Use when user asks if MOM is working, what MOM knows, to check setup, after context reset, or when MOM status is requested.
user-invocable: true
allowed-tools: Bash(mom status*)
---

Run:

```bash
mom status --json
```

Return a concise status summary from parsed JSON (health, routing, vault path/state, memory counts, watcher, available skills). Do **not** print full raw command output.

If output includes sensitive fields (keys, tokens, cookies, passwords, secrets, auth headers), redact and report `[REDACTED]`.

If `mom` is missing from PATH, say MOM is not installed or not on PATH and stop.

If JSON mode is unavailable, run `mom status` and provide a sanitized summary only (never verbatim dump).