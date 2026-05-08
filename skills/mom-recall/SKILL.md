---
name: mom-recall
description: Search MOM's persistent memory. Use when user asks what was decided, discussed, preferred, tried, learned, or remembered about a specific topic.
user-invocable: true
allowed-tools: Bash(mom recall*)
argument-hint: <query>
---

Require one natural-language query from the user.

Run:

```bash
mom recall "<query>"
```

Use Finder through the CLI. Do not call MCP.

Behavior:
- If user asks to show, find, or list memories: print the recall results.
- If user asks a question: answer from the recall results.
- If recall returns no matches: say no matching memories were found.

Output format when matches exist:

```text
Recalled <N> memories:

<direct answer in 2-6 lines>

Sources:
- memoryId: <id-1>
- memoryId: <id-2>
```

Do not:
- Run recall without a query.
- Add flags to `mom recall`.
- Invent answers beyond returned memories.
