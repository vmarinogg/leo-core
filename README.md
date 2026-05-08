<p align="center">
  <img src="assets/logo.png" width="180" alt="MOM">
</p>

<h2 align="center">MOM<br><sub><em>She remembers, so you don't have to_</em></sub></h2>

<p align="center">
  <a href="https://github.com/momhq/mom/releases"><img src="https://img.shields.io/github/v/release/momhq/mom?style=flat-square&color=FFCC2C" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache%202.0-4A6B3A?style=flat-square" alt="License"></a>
  <a href="https://github.com/momhq/mom"><img src="https://img.shields.io/badge/go-1.26+-3B1F0A?style=flat-square" alt="Go 1.26+"></a>
  <a href="https://goreportcard.com/report/github.com/momhq/mom/cli"><img src="https://goreportcard.com/badge/github.com/momhq/mom/cli?style=flat-square" alt="Go Report Card"></a>
</p>


Your AI assistant forgets everything between sessions. You re-explain decisions, conventions, architecture — every time. MOM fixes that.

**MOM** (Memory Oriented Machine) is an open-source CLI that gives AI agents persistent, structured memory. Decisions, constraints, patterns, and learnings — stored in your project, loaded automatically, evolving with every session. Harness-agnostic. On-prem. Schema-validated.

Self-hosting since v0.2 — MOM builds itself with its own memory.

## Quick Start

```bash
# Install via Homebrew
brew tap momhq/tap
brew install mom

# Update
brew update && brew upgrade mom

# Or build from source
git clone https://github.com/momhq/mom.git
cd mom/cli && make install

# Initialize in your project
cd your-project
mom init

# Done. Your agent now has persistent memory.
```

## How It Works

MOM creates a `.mom/` directory in your project — a structured memory layer your AI agent loads at every session.

```
your-project/
├── .mom/                           # MOM's home
│   ├── config.yaml                 # preferences (language, communication mode)
│   ├── schema.json                 # document schema (v2)
│   ├── identity.json               # project identity
│   ├── memory/                     # memory documents (structured JSON)
│   ├── constraints/                # always-active guardrails
│   ├── skills/                     # composable procedures
│   ├── raw/                        # continuous session capture (JSONL)
│   ├── logs/                       # session logs
│   └── cache/
│
├── .mcp.json                       # MCP server config (auto-injected)
├── .claude/CLAUDE.md               # auto-generated boot file for Claude Code
└── your code...
```

You work with your agent. MOM validates, indexes, and delivers memory to the harness. Switch harnesses without losing anything.

## What Makes It Different

**Memory compounds.** Month 6 is structurally richer than month 1. Your agent knows the web of decisions behind your codebase. No one starting from zero can match months of accumulated context.

**Harness-agnostic.** Memory lives in `.mom/`, not in `.claude/` or `.cursor/`. MOM generates the right context for each harness through adapters. Your memory is yours, not locked to a vendor.

**Schema-validated.** Every memory document is tagged, scoped, and promotion-managed. Not a loose Markdown file — a structured, queryable memory with free-form content.

**MCP-first.** MOM delivers context via Model Context Protocol. Agents search, read, and write memory through MCP tools — no file parsing, no guesswork. `.mcp.json` is auto-injected on `mom init`.

**On-prem by default.** Your memory stays in your repo. No cloud dependency. No data leaving your machine.

## Commands

| Command | What it does |
|---------|-------------|
| `mom init` | Interactive onboarding — harness, language, mode |
| `mom status` | Memory summary — document count, tags, health |
| `mom doctor` | Diagnostic checks on `.mom/` health |
| `mom recall <query>` | Search across memory (SQLite FTS5) |
| `mom export` | Export memory to portable directory |
| `mom import` | Import memory (merge or replace) |
| `mom watch` | Watch harness transcripts and ingest turns automatically |
| `mom serve mcp` | Start MCP stdio server |
| `mom serve status` | Show MCP server activity |
| `mom upgrade` | Upgrade `.mom/` to the latest version (preserves memory) |
| `mom uninstall` | Remove all MOM files from this project |
| `mom version` | Print version |

## Supported Harnesses

| Harness | MCP | Watcher | Boot file | Status |
|---------|-----|---------|-----------|--------|
| Claude Code | Yes | Yes | CLAUDE.md | Full support |
| OpenAI Codex | Yes | — | AGENTS.md | Boot file + MCP |
| Windsurf | Yes | Yes | .windsurf/rules/ | Full support |
| Pi | Yes | Yes | AGENTS.md | Full support (extension-based, no native hooks) |

## Current Status

MOM is in active development (v0.14). It works, and it self-hosts — the tool builds itself with its own memory.

What's in v0.14:
- **Progressive recall engine** — searches repo → org → user, curated-first, AND→OR query relaxation when results are sparse
- **Cleaner MCP surface** — `mom_recall` is the single retrieval tool, wired through the recall engine with optional scope pinning
- **Harness tier system** — Native / Fluent / Functional classification per harness, surfaced in `mom doctor`
- **Pi support (Native tier)** — TypeScript extension, `.mcp.json` registration, full watcher integration
- **Constraints simplified** — audited from 6 to 2: `anti-hallucination` and `escalation-triggers`
- **`session-wrap-up` redesigned** — surfaces drafts from disk via `mom_recall`, not from context window
- **`mom init` as config manager** — reconciles harnesses on reinit (enable new, disable removed)
- **`harness` rename** — `runtime` → `harness` throughout config, CLI flags, and internals
- SQLite FTS5 search with content-first column weights
- Global watch daemon (launchd / systemd) for all registered projects
- Multi-repo support with scope-based memory (repo → org → user)
- Homebrew installation with automated tap updates

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, conventions, and how to submit PRs.

If you work with AI agents and feel the amnesia pain — issues, feedback, and honest criticism are welcome.

## License

[Apache 2.0](LICENSE)
