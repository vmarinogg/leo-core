<div align="center">

# MOM Skills for Claude Code

User-invocable skills that expose MOM’s CLI-first memory workflows.

</div>

## What’s included

This plugin provides 3 skills:

- `/mom:mom-status` — check MOM health and vault state (sanitized summary)
- `/mom:mom-recall <query>` — search persistent memory
- `/mom:mom-wrap-up` — review and curate draft memories


## Install

From your project root:

```bash
# install from registry/repo source
/plugin install momhq/mom
```

Or test locally during development:

```bash
claude --plugin-dir ./skills
```

Then reload plugins in Claude Code:

```text
/reload-plugins
```

## Usage examples

```text
/mom:mom-status
/mom:mom-recall decision about auth boundary
/mom:mom-wrap-up
```

## Plugin layout

```text
skills/
├── .claude-plugin/plugin.json
├── mom-status/SKILL.md
├── mom-recall/SKILL.md
└── mom-wrap-up/SKILL.md
```

## Behavior and safety

- `mom-status` returns a concise parsed summary and avoids raw verbatim dumps
- Sensitive fields should be redacted if ever present (`[REDACTED]`)
- `mom-wrap-up` requires explicit user approval before running `mom curate`
