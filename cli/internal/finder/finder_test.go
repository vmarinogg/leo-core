package finder_test

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/archtest"
	"github.com/momhq/mom/cli/internal/finder"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
)

// openFinder opens a temp vault with Librarian+Logbook migrations and
// returns a Finder bound to a fresh Librarian. Logbook's migrations
// are included so the architectural shape matches production.
func openFinder(t *testing.T) (*finder.Finder, *librarian.Librarian) {
	t.Helper()
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...); sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migs)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	lib := librarian.New(v)
	return finder.New(lib), lib
}

// promote sets a memory to curated. Helper.
func promote(t *testing.T, lib *librarian.Librarian, id string) {
	t.Helper()
	state := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
		t.Fatalf("promote %s: %v", id, err)
	}
}

func insertMemory(t *testing.T, lib *librarian.Librarian, sessionID, text string, tags ...string) string {
	t.Helper()
	id, err := lib.Insert(librarian.InsertMemory{
		Content:   `{"text":"` + text + `"}`,
		Summary:   text,
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	for _, name := range tags {
		tagID, err := lib.UpsertTag(name)
		if err != nil {
			t.Fatalf("UpsertTag %q: %v", name, err)
		}
		if err := lib.LinkTag(id, tagID); err != nil {
			t.Fatalf("LinkTag: %v", err)
		}
	}
	return id
}

// ── Validation ───────────────────────────────────────────────────────────────

func TestRecall_RejectsEmptyQuery(t *testing.T) {
	f, _ := openFinder(t)
	if _, err := f.Recall(finder.Options{Query: ""}); !errors.Is(err, finder.ErrEmptyQuery) {
		t.Fatalf("err = %v, want ErrEmptyQuery", err)
	}
	if _, err := f.Recall(finder.Options{Query: "   "}); !errors.Is(err, finder.ErrEmptyQuery) {
		t.Fatalf("whitespace err = %v, want ErrEmptyQuery", err)
	}
}

// ── FTS basics ───────────────────────────────────────────────────────────────

func TestRecall_FindsByContent(t *testing.T) {
	f, lib := openFinder(t)
	id := insertMemory(t, lib, "s", "deploy postgres canary")
	promote(t, lib, id)

	got, err := f.Recall(finder.Options{Query: "deploy"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("got %v, want [%q]", got, id)
	}
}

func TestRecall_TreatsHyphenatedQueryAsNaturalLanguage(t *testing.T) {
	f, lib := openFinder(t)
	id := insertMemory(t, lib, "s", "wrap up skill validation")
	promote(t, lib, id)

	got, err := f.Recall(finder.Options{Query: "wrap-up"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("got %v, want [%q]", got, id)
	}
}

func TestRecall_TreatsFTSOperatorsAsNaturalLanguage(t *testing.T) {
	f, lib := openFinder(t)
	id := insertMemory(t, lib, "s", "literal OR operator note")
	promote(t, lib, id)

	got, err := f.Recall(finder.Options{Query: "literal OR operator"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("got %v, want [%q]", got, id)
	}
}

func TestRecall_FindsBySummary(t *testing.T) {
	f, lib := openFinder(t)
	id, err := lib.Insert(librarian.InsertMemory{
		Content:   `{"text":"unrelated body"}`,
		Summary:   "production outage 2026-04-15",
		SessionID: "s",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	promote(t, lib, id)

	got, _ := f.Recall(finder.Options{Query: "outage", IncludeDrafts: true})
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("got %v, want [%q]", got, id)
	}
}

// ── Tier escalation ──────────────────────────────────────────────────────────

func TestRecall_EscalatesToDraftsWhenCuratedThin(t *testing.T) {
	f, lib := openFinder(t)
	// Two drafts, no curated. Default IncludeDrafts=false should still
	// return them via tier escalation.
	a := insertMemory(t, lib, "s", "deploy alpha")
	b := insertMemory(t, lib, "s", "deploy beta")

	got, _ := f.Recall(finder.Options{Query: "deploy"})
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.ID] = true
		if r.Tier == finder.TierCurated {
			t.Errorf("draft result tagged tier=curated: %v", r)
		}
	}
	if !ids[a] || !ids[b] {
		t.Fatalf("got %v, want both %q and %q", ids, a, b)
	}
}

func TestRecall_PrefersCuratedWhenAvailable(t *testing.T) {
	f, lib := openFinder(t)
	// Insert thresholdLow (default 5) curated rows so the curated pass
	// is satisfied and Finder stops there — no escalation to drafts.
	curatedIDs := map[string]bool{}
	for i := 0; i < 5; i++ {
		id := insertMemory(t, lib, "s", "deploy semantic curated "+string(rune('a'+i)))
		promote(t, lib, id)
		curatedIDs[id] = true
	}
	// And one draft we expect NOT to appear (escalation should not
	// trigger because curated is full).
	draftID := insertMemory(t, lib, "s", "deploy draft variant")

	got, _ := f.Recall(finder.Options{Query: "deploy"})
	for _, r := range got {
		if r.Tier != finder.TierCurated {
			t.Errorf("unexpected tier %q on hit %q (curated tier should be sufficient)", r.Tier, r.ID)
		}
		if r.ID == draftID {
			t.Errorf("draft hit %q surfaced despite curated being full", draftID)
		}
	}
	if len(got) != 5 {
		t.Fatalf("got %d hits, want 5", len(got))
	}
	_ = curatedIDs
}

// ── AND→OR relaxation ────────────────────────────────────────────────────────

func TestRecall_ANDPassPrecisionMultiToken(t *testing.T) {
	// Memory A contains both terms; B contains only one. The AND
	// pass should match A and only A. Forcing thresholdLow=1 makes
	// the AND pass satisfy on its own — without that, the OR pass
	// runs too and B sneaks in, which would mask any AND-precision
	// regression.
	f, lib := openFinder(t)
	f = f.WithThresholdLow(1)
	a := insertMemory(t, lib, "s", "deploy postgres canary")
	b := insertMemory(t, lib, "s", "deploy redis cluster")

	got, err := f.Recall(finder.Options{
		Query:         "deploy postgres",
		IncludeDrafts: true,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one AND-precise match, got %d: %v", len(got), got)
	}
	if got[0].ID != a {
		t.Fatalf("AND-precise match = %q, want %q (B=%q sneaked in via OR)", got[0].ID, a, b)
	}
	// Tier must be the AND tier, not the OR tier — proves the AND
	// pass actually satisfied and we didn't fall through.
	if got[0].Tier != finder.TierDraft {
		t.Errorf("Tier = %q, want %q (AND pass for IncludeDrafts=true)", got[0].Tier, finder.TierDraft)
	}
}

// TestRecall_IncludeDraftsTrue_SkipsCuratedTier locks the contract
// that IncludeDrafts=true bypasses the curated-only passes entirely.
// Every result must carry a draft-tier label, never a curated one.
func TestRecall_IncludeDraftsTrue_SkipsCuratedTier(t *testing.T) {
	f, lib := openFinder(t)
	curated := insertMemory(t, lib, "s", "deploy curated")
	promote(t, lib, curated)
	draft := insertMemory(t, lib, "s", "deploy draft")

	got, err := f.Recall(finder.Options{
		Query:         "deploy",
		IncludeDrafts: true,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, r := range got {
		if r.Tier == finder.TierCurated || r.Tier == finder.TierCuratedOR {
			t.Errorf("IncludeDrafts=true must skip curated passes; got tier=%q on %q", r.Tier, r.ID)
		}
	}
	// Both rows still surface — the curated row is still a memory,
	// just queried under the draft tier (no promotion-state filter).
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.ID] = true
	}
	if !ids[curated] || !ids[draft] {
		t.Errorf("expected both rows, got %v", ids)
	}
}

// TestRecall_WithThresholdLow_StopsEscalationEarlier locks the
// configurable-threshold contract: at thresholdLow=1, a single
// curated match satisfies the first pass and the loop stops; lower
// tiers (drafts) are not searched.
func TestRecall_WithThresholdLow_StopsEscalationEarlier(t *testing.T) {
	f, lib := openFinder(t)
	f = f.WithThresholdLow(1)

	curated := insertMemory(t, lib, "s", "deploy curated")
	promote(t, lib, curated)
	draft := insertMemory(t, lib, "s", "deploy draft")

	got, err := f.Recall(finder.Options{Query: "deploy"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	// thresholdLow=1 → curated AND yields 1 → satisfies → stop.
	// The draft row must NOT appear.
	for _, r := range got {
		if r.ID == draft {
			t.Errorf("draft %q surfaced despite thresholdLow=1 satisfying curated", draft)
		}
		if r.Tier != "curated" {
			t.Errorf("Tier = %q, want curated (early stop)", r.Tier)
		}
	}
}

func TestRecall_SingleTokenSkipsORPass(t *testing.T) {
	// Single-token queries are unchanged from pre-0.30 — no OR pass.
	// We can observe this by checking that the resulting tier never
	// carries the "-or" suffix.
	f, lib := openFinder(t)
	id := insertMemory(t, lib, "s", "deploy canary")
	promote(t, lib, id)

	got, _ := f.Recall(finder.Options{Query: "deploy"})
	for _, r := range got {
		if strings.HasSuffix(r.Tier, "-or") {
			t.Errorf("single-token query produced OR-pass tier %q", r.Tier)
		}
	}
}

// ── Filters ──────────────────────────────────────────────────────────────────

func TestRecall_MultiTagAND(t *testing.T) {
	f, lib := openFinder(t)
	// a has both "deploy" and "postgres"; b has only "deploy"; c has
	// only "postgres". Multi-tag AND should return only a.
	a := insertMemory(t, lib, "s", "alpha", "deploy", "postgres")
	_ = insertMemory(t, lib, "s", "beta", "deploy")
	_ = insertMemory(t, lib, "s", "gamma", "postgres")

	got, _ := f.Recall(finder.Options{
		Query:         "alpha OR beta OR gamma", // FTS broadens to all three
		Tags:          []string{"deploy", "postgres"},
		IncludeDrafts: true,
	})
	if len(got) != 1 || got[0].ID != a {
		t.Fatalf("got %v, want [%q]", got, a)
	}
}

func TestRecall_SessionIDNarrowing(t *testing.T) {
	f, lib := openFinder(t)
	want := insertMemory(t, lib, "s-1", "shared word here")
	_ = insertMemory(t, lib, "s-2", "shared word here too")

	got, _ := f.Recall(finder.Options{
		Query:         "shared",
		SessionID:     "s-1",
		IncludeDrafts: true,
	})
	if len(got) != 1 || got[0].ID != want {
		t.Fatalf("got %v, want [%q]", got, want)
	}
}

// ── Ordering / scoring ───────────────────────────────────────────────────────

func TestRecall_BM25HeavierOnContentThanSummary(t *testing.T) {
	// ADR 0007: column weights 0/2/10 over id/summary/content_text. A
	// query whose token appears in BOTH content and summary on memory
	// A but only in summary on memory B should rank A higher.
	f, lib := openFinder(t)
	idA, err := lib.Insert(librarian.InsertMemory{
		Content:   `{"text":"deploy production deploy production deploy production"}`,
		Summary:   "deploy",
		SessionID: "s",
	})
	if err != nil {
		t.Fatalf("Insert A: %v", err)
	}
	idB, err := lib.Insert(librarian.InsertMemory{
		Content:   `{"text":"unrelated body content"}`,
		Summary:   "deploy",
		SessionID: "s",
	})
	if err != nil {
		t.Fatalf("Insert B: %v", err)
	}
	promote(t, lib, idA)
	promote(t, lib, idB)

	got, _ := f.Recall(finder.Options{Query: "deploy"})
	if len(got) < 2 {
		t.Fatalf("want both hits, got %d", len(got))
	}
	if got[0].ID != idA {
		t.Errorf("A should rank above B (content weight > summary weight); got order %q, %q",
			got[0].ID, got[1].ID)
	}
}

// ── Architecture ─────────────────────────────────────────────────────────────

// TestFinder_DoesNotImportVault enforces "Finder reads through
// Librarian." Direct-import only — transitive through Librarian is
// expected.
func TestFinder_DoesNotImportVault(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/cli/internal/vault",
	)
}
