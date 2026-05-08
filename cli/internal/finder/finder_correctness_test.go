package finder_test

import (
	"errors"
	"testing"

	"github.com/momhq/mom/cli/internal/finder"
	"github.com/momhq/mom/cli/internal/librarian"
)

// TestRecall_AllPassesThinReturnsLastTier locks the natural-loop-exit
// path. The previous shape ran the last pass twice when no pass met
// thresholdLow — wasted query plus a race window where the duplicate
// could see different rows than the first call. The fix is that the
// loop tracks the last pass's hits and returns them at exit, no
// second SearchMemories call.
//
// Test: insert one draft (below default thresholdLow=5). Every pass
// returns 1 row, none meet threshold, the loop exits naturally. The
// returned tier must be the LAST pass's tier ("draft-or" for a
// multi-token query, "draft" for single-token), and the returned
// memory must be the inserted one.
func TestRecall_AllPassesThinReturnsLastTier(t *testing.T) {
	t.Run("multi-token", func(t *testing.T) {
		f, lib := openFinder(t)
		mid := insertMemory(t, lib, "s", "uniqueword anotherword")

		got, err := f.Recall(finder.Options{
			Query: "uniqueword anotherword",
			// IncludeDrafts=false → curated AND, curated OR, draft AND,
			// draft OR. Every pass returns the one matching draft, none
			// hit thresholdLow=5.
		})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if len(got) != 1 || got[0].ID != mid {
			t.Fatalf("got %v, want [%q]", got, mid)
		}
		// Last pass is draft-or for multi-token + IncludeDrafts=false.
		if got[0].Tier != finder.TierDraftOR {
			t.Errorf("Tier = %q, want %q (last pass for multi-token)", got[0].Tier, finder.TierDraftOR)
		}
	})

	t.Run("single-token", func(t *testing.T) {
		f, lib := openFinder(t)
		mid := insertMemory(t, lib, "s", "uniqueword")

		got, err := f.Recall(finder.Options{Query: "uniqueword"})
		if err != nil {
			t.Fatalf("Recall: %v", err)
		}
		if len(got) != 1 || got[0].ID != mid {
			t.Fatalf("got %v, want [%q]", got, mid)
		}
		// Single-token skips OR passes; last pass is "draft".
		if got[0].Tier != finder.TierDraft {
			t.Errorf("Tier = %q, want %q (last pass for single-token)", got[0].Tier, finder.TierDraft)
		}
	})
}

// TestRecall_EmptyTagInTags_RejectedAtLibrarian locks the lesson:
// previously, Tags=["deploy", ""] silently produced WHERE name IN
// ("deploy", "") with COUNT(DISTINCT) = 2, returning zero rows with
// no signal that the input was malformed. The fix rejects at the SQL
// composer with ErrEmptyArg.
func TestRecall_EmptyTagInTags_RejectedAtLibrarian(t *testing.T) {
	f, lib := openFinder(t)
	insertMemory(t, lib, "s", "deploy postgres", "deploy")

	cases := [][]string{
		{""},
		{"deploy", ""},
		{"  "},
		{"deploy", " "},
	}
	for _, tags := range cases {
		_, err := f.Recall(finder.Options{
			Query: "deploy",
			Tags:  tags,
		})
		if !errors.Is(err, librarian.ErrEmptyArg) {
			t.Errorf("tags=%v: err = %v, want ErrEmptyArg", tags, err)
		}
	}
}
