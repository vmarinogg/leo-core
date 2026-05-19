# v0.50 — Recall overhaul (deferred from v0.40)

Status: backlog
Owner: TBD
Trigger: pair with the v0.50 architectural pass instead of patching v0.40

## Why

During v0.40 local E2E, recall ranking surfaced two real UX gaps:

1. **BM25 over-weights long curated memories sharing common tokens** with the query, above short drafts that contain the exact phrase.
   Example: query `claude scoped raven 2841` returned curated memories about
   scoping/readiness above the actual marker draft containing the exact phrase.
   Query `raven 2841` (distinct tokens only) put the marker on top.
2. **Drafts had no `Summary`**, so recall output showed blank rows and agents
   concluded "no specific match" even when the marker was right there.
   Patched in v0.40 by falling back to a content excerpt (cli/internal/cmd/recall.go).
   The deeper signal — that the user needs to *see* matching content, not a
   row id — is unaddressed by ranking alone.

## Cheap, isolated heuristic (could land earlier if needed)

- When the verbatim query string appears in `content`, boost that row above
  pure-token BM25 hits. Conservative version: only if query length ≥ N chars
  to avoid trivial substring boosts.

## Full v0.50 scope (likely what we want)

- Recall ranking signals: recency, project, actor, exact-phrase boost,
  landmark / centrality weighting, draft-vs-curated weighting.
- Hybrid lexical + semantic retrieval (FTS + embeddings) with a single
  blended score the CLI/agents can reason about.
- Snippet selection for the displayed line — show the actual match window,
  not the first 200 chars of the document.
- Agent-facing output schema (`mom recall --json`?) so skills like
  `/mom-recall` can reason about hits (project, actor, score, snippet)
  instead of parsing a table.
- Project scope defaults review: when to include null-project rows, when
  to require `--strict-project`, how to communicate the chosen scope.

## Out of scope (do not bundle)

- Curation flow (mom curate / mom-wrap-up) — orthogonal.
- Indexing pipeline rebuild — unless semantic retrieval forces it.

## Acceptance hint

E2E sanity: marker drafts created in a project must rank above unrelated
curated memories for any reasonable query that contains the marker tokens,
including the full literal phrase.
