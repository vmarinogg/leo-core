<div align="center">

<img src="assets/logo.svg" alt="MOM" width="120" />

# _mom_

_Memory Oriented Machine — she remembers, so you don't have to._

<p>
  <a href="https://github.com/momhq/mom/releases"><img src="https://img.shields.io/github/v/release/momhq/mom?style=flat-square&color=FFCC2C" alt="Release"></a>
  <a href="https://github.com/momhq/mom/actions"><img src="https://img.shields.io/github/actions/workflow/status/momhq/mom/ci.yml?style=flat-square&label=CI" alt="CI"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/Go-1.26+-3B1F0A?style=flat-square" alt="Go 1.26+"></a>
  <a href="https://github.com/momhq/homebrew-tap"><img src="https://img.shields.io/badge/Homebrew-momhq/tap-4A6B3A?style=flat-square" alt="Homebrew tap"></a>
  <a href="https://github.com/momhq/mom/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-4A6B3A?style=flat-square" alt="Apache 2.0 license"></a>
  <a href="https://skills.sh/momhq/mom"><img src="https://skills.sh/b/momhq/mom" alt="skills.sh installs"></a>
</p>

[Install](#install) • [Quick start](#quick-start) • [How it works](#how-it-works) • [Typical workflow](#typical-workflow) • [Harness support](#harness-support)

</div>

_Mom_ gives AI coding agents persistent memory across sessions, projects, and tools.

Instead of re-explaining architecture, decisions, conventions, and constraints every time you start a new chat, _mom_ stores them in a local SQLite vault and makes them available inside the agents you already use.

> [!IMPORTANT]
> `v0.40.0-alpha` is the current public alpha. Pi, Claude Code, and Codex flows are validated end-to-end. Windsurf support was retired in this release (see [ADR — Windsurf retirement](https://github.com/momhq/mom/pull/343)).

## Why _mom_?

AI agents are useful, but they forget.

_Mom_ is the memory layer beside them:

- **Persistent** — memory survives `/clear`, compaction, restarts, and tool switches.
- **Local-first** — the central vault lives at `$HOME/.mom/mom.db`.
- **Harness-agnostic** — Pi, Claude Code, and Codex are integration targets, not storage silos.
- **Agent-integrated** — memory is available through _mom_ skills and native harness integrations.
- **MCP-backed** — MCP remains available for startup, discovery, and fallback access.
- **Continuously recorded** — supported harness transcripts are watched and distilled into draft memories.

## Install

### Homebrew

```bash
brew install momhq/tap/mom
```

To upgrade:

```bash
brew update && brew upgrade mom && mom version
```

### From source

```bash
git clone https://github.com/momhq/mom.git
cd mom/cli
make install
```

## Quick start

Initialize _mom_ once:

```bash
mom init
```

_Mom_ will create the global vault, configure detected harness integrations, install _mom_ skills where supported, and start the global watch daemon.

Then open your agent and work normally. _Mom_ runs in the background, watches supported transcript sources, and keeps useful context available through skills:

```text
/mom-status
/mom-recall the decision about the auth boundary
/mom-wrap-up
```

## How it works

_Mom_ keeps one central memory vault and adapts it to each harness.

```text
AI harnesses
  ├─ Pi extension
  ├─ Claude Code hooks + skills
  ├─ Codex hooks + MCP
  └─ MCP fallback
        │
        ▼
mom CLI + watcher daemon
        │
        ▼
$HOME/.mom/mom.db
  ├─ memories
  ├─ tags
  ├─ entities
  ├─ import mappings
  └─ operational events
```

## Typical workflow

After installing _mom_, open your agent and work normally.

_Mom_ watches supported transcript sources in the background. Useful turns become draft memories. After a long session, or whenever you want to preserve recent context, ask your agent to run:

```text
/mom-wrap-up
```

The skill reviews recent drafts with you and helps curate the memories worth keeping.

Later, when you need something _mom_ has seen before, ask your agent:

```text
/mom-recall deployment rollback procedure
```

_Mom_ searches both draft and curated memory so the agent can recover decisions, conventions, and context without you re-explaining them.

To check that _mom_ is connected:

```text
/mom-status
```

### Explore with Lens

For a visual view of sessions, memories, and privacy-projected operational events, run:

```bash
mom lens
```

Lens is local and reads from the central _mom_ vault.

### Export and import memory

For backup, migration, or sharing a local vault snapshot:

```bash
mom export
mom import <path>
```

`mom export` writes central SQLite table dumps to `$HOME/.mom/exports/<timestamp>/`. `mom import` safely merges new exports or legacy JSON memory directories and skips existing rows.

## Harness support

| Harness | Current status | Notes |
| --- | --- | --- |
| Pi | Validated | Native extension support via `pi install npm:pi-mom`. Gold standard for _mom_. |
| Claude Code | Validated | Fluent speaker. Provides all the necessary tools _mom_ needs. |
| Codex | Validated | Hooks, MCP, and per-turn project scoping working end-to-end. |

> [!NOTE]
> _Mom_ uses **harness** to mean the agent framework around the model: tools, hooks, transcripts, prompt files, and MCP configuration.

## Upgrade from older _mom_ installs

If you already have an older _mom_ install:

```bash
mom upgrade --dry-run
mom upgrade
```

Upgrade can import legacy memories, import legacy operational logs, remove known generated legacy files, preserve custom files, clean obsolete hook commands, and install or update skills as a soft-fail step.

## Data and privacy

_Mom_ is local-first.

- The default vault is `$HOME/.mom/mom.db`.
- `MOM_VAULT=/path/to/mom.db` overrides the vault for tests or isolated runs.
- Lens and operational logs use privacy-projected metadata.
- Raw tool arguments, raw user text, shell command arguments, query strings, paths, and flags are not stored as operational log detail.
- Explicit record flows reject invented session IDs; harness session IDs must come from the harness.

## Troubleshooting

### Check installation

```bash
mom version
mom status
mom doctor
```

### Watcher is not seeing new sessions

```bash
mom watch --status
mom watch --sweep
```

_Mom_ canonicalizes project paths, including macOS `/tmp` and `/private/tmp`, so watcher state should not split across symlinked aliases.

### Skills are missing

Run init or upgrade again:

```bash
mom init
# or
mom upgrade
```

Skills install is a soft-fail step, so _mom_ can be usable even if the external skills installer is temporarily unavailable.

## Project layout

```text
.
├── assets/                 # logo and brand assets
├── cli/                    # Go CLI, MCP server, watcher, Lens server
├── skills/                 # _mom_ slash skills
├── adr/                    # architecture decisions
├── prd/                    # product requirements
├── Formula/mom.rb          # Homebrew formula
└── .github/workflows/      # CI and release automation
```

## Resources

- [Latest release](https://github.com/momhq/mom/releases/latest)
- [Issues](https://github.com/momhq/mom/issues)
- [Homebrew tap](https://github.com/momhq/homebrew-tap)
- [Privacy policy](https://github.com/momhq/mom/blob/main/PRIVACY.md)
