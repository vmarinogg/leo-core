---
name: mom-status
description: Show MOM's current state. Use when user asks if MOM is working, what MOM knows, to check setup, after context reset, or when MOM status is requested.
user-invocable: true
allowed-tools: Bash(mom status*)
---

Run:

```bash
mom status
```

Print the raw output verbatim. Do not summarize, reinterpret, or call MCP.

If `mom` is missing from PATH, say MOM is not installed or not on PATH and stop.
