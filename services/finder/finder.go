// Package finder is the v0.30 search service over Vault. Renamed from
// "Recall" to avoid colliding with the user-facing mom_recall / mom
// recall verbs.
//
// Finder reads through Librarian — it never imports the Vault package
// directly. The architectural rule is locked by an import-graph test
// in finder_test.go.
//
// Finder combines:
//   - FTS5 ranking with column weights from ADR 0007 (0/2/10 over
//     id/summary/content_text).
//   - AND→OR query relaxation from ADR 0008. Single-token queries are
//     unchanged; multi-token queries first try AND (precision) and
//     widen to OR (recall) only when the precise pass returned too few
//     results.
//   - Curated/draft tier escalation from ADR 0006 (quality dimension).
//     The progressive scope-chain dimension of ADR 0006 is gone with
//     the scope chain — there is one vault.
//
// Finder does NOT re-run capture-time filters (ADR 0014) — those live
// in Drafter and apply at the write boundary.
package finder

import (
	"errors"
	"fmt"
	"strings"

	"github.com/momhq/mom/storage/librarian"
)

// ErrEmptyQuery is returned by Recall when the input query is empty
// after trimming. The lesson from the previous attempt: empty query
// must reject loudly with "query is required" rather than silently
// returning "no memories matched" — buggy callers were getting
// misdiagnosed as cold-cache.
var ErrEmptyQuery = errors.New("finder: query is required")

// Options narrows a Recall call. Query is required; everything else is
// optional. Limit defaults to 20.
type Options struct {
	Query         string
	Tags          []string // memory must have ALL these tags
	SessionID     string
	IncludeDrafts bool // when false (default), Finder applies tier escalation
	Limit         int

	// ProjectId restricts results to the named project (ADR 0016).
	// Empty disables the project filter — searches every project.
	ProjectId string
	// StrictProject excludes NULL project_id rows when ProjectId is set.
	// Defaults to false (legacy memories remain findable in scoped queries).
	StrictProject bool
}

// Tier names the escalation pass that surfaced a Result. The four
// values are the cross-product of {curated, draft} × {AND, OR}; the
// AND tier name has no suffix, the OR tier name carries "-or."
//
// Constants centralise the labels so renaming or extending the
// vocabulary stays a one-place edit instead of grep-and-replace
// across pipeline + tests.
const (
	TierCurated   = "curated"
	TierCuratedOR = "curated-or"
	TierDraft     = "draft"
	TierDraftOR   = "draft-or"
)

// Result is one ranked memory hit. Score is the BM25 score from FTS5
// (lower = more relevant in SQLite's bm25 convention); Tier is one of
// the package Tier* constants.
type Result struct {
	librarian.Memory
	Score float64
	Tier  string
}

// Finder is the search service. Construct with New.
type Finder struct {
	lib *librarian.Librarian

	// thresholdLow is the result count below which Finder escalates to
	// the next pass (more drafts, then OR-relaxation). Tuned for
	// personal-vault scale; configurable via WithThresholdLow.
	thresholdLow int
}

// New returns a Finder backed by the given Librarian.
func New(lib *librarian.Librarian) *Finder {
	return &Finder{lib: lib, thresholdLow: 5}
}

// WithThresholdLow configures the minimum result count that prevents
// further escalation. Defaults to 5.
func (f *Finder) WithThresholdLow(n int) *Finder {
	if n > 0 {
		f.thresholdLow = n
	}
	return f
}

// Recall executes the search with relaxation + tier escalation. Returns
// ranked results (best first) up to opts.Limit (default 20).
//
// Pipeline (each pass stops if it yields >= thresholdLow OR Limit):
//  1. curated + AND  — most precise.
//  2. curated + OR   — multi-token queries only; widen recall while
//     keeping the curated tier.
//  3. drafts + AND   — drop the curated gate.
//  4. drafts + OR    — multi-token queries only; widest pass.
//
// IncludeDrafts=true skips the curated-only passes.
func (f *Finder) Recall(opts Options) ([]Result, error) {
	if strings.TrimSpace(opts.Query) == "" {
		return nil, ErrEmptyQuery
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	passes := buildPasses(opts)

	// Track the most-recent (widest) results so we can return them on
	// natural loop exit without re-running SearchMemories. The
	// previous shape ran the last pass twice in the all-thin case —
	// wasted query plus a race window between the two calls.
	var (
		lastHits []librarian.SearchedMemory
		lastTier string
	)
	for _, p := range passes {
		hits, err := f.lib.SearchMemories(librarian.SearchFilter{
			FTSQuery:       p.ftsQ,
			Tags:           opts.Tags,
			SessionID:      opts.SessionID,
			PromotionState: p.state,
			Limit:          limit,
			ProjectId:      opts.ProjectId,
			StrictProject:  opts.StrictProject,
		})
		if err != nil {
			return nil, fmt.Errorf("Recall %s pass: %w", p.tier, err)
		}
		if len(hits) >= f.thresholdLow || len(hits) >= limit {
			return resultsFrom(hits, p.tier), nil
		}
		lastHits, lastTier = hits, p.tier
		// Keep going — this pass yielded too few. The natural exit
		// path returns these last (widest) results.
	}
	return resultsFrom(lastHits, lastTier), nil
}

// pass describes one search invocation in the escalation pipeline.
type pass struct {
	ftsQ  string
	state string // "" = any (drops the curated gate)
	tier  string
}

// buildPasses returns the ordered list of search passes for opts.
// Order is curated→draft (quality dim, ADR 0006) and within each tier
// AND→OR (precision→recall, ADR 0008). Single-token queries skip OR
// passes (the AND/OR distinction is meaningless for one term).
// IncludeDrafts=true skips the curated-only passes entirely; the
// caller is asking for the widest matrix.
func buildPasses(opts Options) []pass {
	multiToken := tokenCount(opts.Query) > 1
	andQ := normaliseFTSQuery(opts.Query)

	out := make([]pass, 0, 4)
	if !opts.IncludeDrafts {
		out = append(out, pass{andQ, "curated", TierCurated})
		if multiToken {
			out = append(out, pass{buildORQuery(opts.Query), "curated", TierCuratedOR})
		}
	}
	out = append(out, pass{andQ, "", TierDraft})
	if multiToken {
		out = append(out, pass{buildORQuery(opts.Query), "", TierDraftOR})
	}
	return out
}

func resultsFrom(hits []librarian.SearchedMemory, tier string) []Result {
	out := make([]Result, 0, len(hits))
	for _, h := range hits {
		out = append(out, Result{Memory: h.Memory, Score: h.Score, Tier: tier})
	}
	return out
}

// tokens splits the query on whitespace, trimming each. Empty tokens
// are removed.
func tokens(query string) []string {
	parts := strings.Fields(query)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// tokenCount is the number of FTS5-tokenisable terms in the query.
// Used to decide whether OR-relaxation is meaningful.
func tokenCount(query string) int {
	return len(tokens(query))
}

// normaliseFTSQuery builds a safe FTS5 AND query from natural-language input.
// Quoting every whitespace-delimited token preserves FTS5's default AND
// semantics while preventing punctuation such as hyphens from being parsed as
// operators or column selectors.
func normaliseFTSQuery(query string) string {
	tt := tokens(query)
	parts := make([]string, len(tt))
	for i, t := range tt {
		parts[i] = quoteFTS(t)
	}
	return strings.Join(parts, " ")
}

// buildORQuery rewrites the query as an OR of its tokens. Each token
// is quoted to preserve FTS5 phrase semantics for any embedded
// punctuation or operators the input might contain.
func buildORQuery(query string) string {
	tt := tokens(query)
	if len(tt) == 0 {
		return ""
	}
	parts := make([]string, len(tt))
	for i, t := range tt {
		parts[i] = quoteFTS(t)
	}
	return strings.Join(parts, " OR ")
}

func quoteFTS(s string) string {
	return `"` + escapeFTS(s) + `"`
}

// escapeFTS doubles any embedded double-quote so the returned string
// is safe inside an FTS5 phrase.
func escapeFTS(s string) string {
	return strings.ReplaceAll(s, `"`, `""`)
}
