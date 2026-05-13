# 0011 — Substance immutability; operational metadata is mutable

A memory row carries two kinds of fields. Some describe *what was captured* — the content, the summary read out of it, the moment and circumstances of capture. Others describe *how MOM is currently filing it* — promotion state, landmark flag, centrality, the type assignment from ADR 0012, the edges to tags and entities. The current model treats both as equally writable, which means a routine recuration is indistinguishable from a content rewrite, and there is no way to assert "this memory still reflects what was originally captured."

The schema enforces a hard boundary.

**Substance fields are immutable after creation.** On `memories`: `id`, `content`, `summary`, `created_at`, `session_id`, `project_id` (added by ADR 0016), `provenance_actor`, `provenance_trigger_event`, `provenance_source_type`. Once written, no code path updates these. Any change to substance is a new memory, not an edit.

**Operational metadata is mutable.** On `memories`: `type` (ADR 0012), `promotion_state`, `landmark`, `centrality_score`. Off the row: edges in `memory_tags` and `memory_entities`. These can be set, changed, added, or removed freely as curation evolves.

The boundary is enforced by the write API — substance columns are never updated by any normal path. The only exception is an explicit, audited correction tool (out of scope for this ADR), used rarely and visibly.

## Consequences

- "What did this turn actually contain?" has a permanent answer. Recall results carry the captured substance, not a later rewrite.
- Curation is cheap and frequent: retagging, retyping, promoting draft → curated, marking landmarks, and adjusting centrality are all routine and don't disturb substance.
- Correcting a captured memory means creating a new one and pointing the old one at it via the future `relations` table (`supersedes`). This is heavier than an in-place edit, which is the point.
- Soft-retirement and "no longer current" semantics are deferred to the relations layer (v0.X+), where temporal validity will live on edges (`valid_from` / `valid_until`) rather than on the memory row. v0.30 ships without a node-level retirement field.
- `summary` is treated as substance because it is read out of content at capture time and is what most surfaces (recall results, lens lists, wrap-up) display. If a summary is wrong, the fix is supersede, not edit.
- Any future feature that wants to "edit a memory" must either touch only operational fields or supersede.

## Considered alternatives

- **All fields mutable; rely on a history table.** Rejected: requires per-memory history rows and still loses the conceptual distinction. The boundary is the value, not the audit log.
- **All fields immutable; everything is a new memory.** Rejected: retagging, repromoting, or marking a landmark would mint new memories, which inflates the vault and severs operational continuity.
- **Treat `summary` as operational (regenerable).** Rejected: summary is the human-facing read of content at the moment of capture. Letting it drift independently of content makes recall results unreliable. If it needs to change, supersede.
- **Mutable substance, immutable operational metadata.** Rejected: the inverse of what's actually useful. Operational metadata is exactly the part that should evolve with curation.
