package drafter

import (
	"regexp"
	"strings"
)

// softTurn is the slice of a turn the soft filter cares about. The
// Drafter Worker maps the bus-event payload onto this shape before
// calling isNoise. Keeping the input narrow makes the filter pure
// and easy to test in isolation.
type softTurn struct {
	Role                   string
	Text                   string
	ToolCount              int // total tool_calls in the turn
	CodebaseWriteToolCount int // subset of ToolCount in the codebase_write category
}

// thinkingBlockRE matches Claude-style chain-of-thought blocks.
// `(?s)` makes `.` match newlines so multi-line monologue is
// captured as one block.
var thinkingBlockRE = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)

// ackPhrases is the bare-acknowledgement vocabulary. Lowercased,
// trimmed; the soft filter compares the cleaned turn text against
// this set.
var ackPhrases = map[string]struct{}{
	"ok": {}, "okay": {}, "sure": {}, "yes": {}, "yeah": {}, "yep": {},
	"no": {}, "nope": {}, "thanks": {}, "thx": {}, "ty": {}, "thank you": {},
	"got it": {}, "gotcha": {}, "sounds good": {}, "great": {}, "cool": {},
	"nice": {}, "perfect": {}, "👍": {}, "❤️": {}, "🙏": {},
}

// minMeaningfulWords is the threshold below which a turn is too thin
// to be a memory regardless of category. 3 is permissive — a real
// turn usually contains many more substantive words.
const minMeaningfulWords = 3

// isNoise classifies a turn as noise per ADR 0014's soft filter
// rules. The filter is CHEAP — it errs slightly toward keeping
// signal because the user has the explicit-write override
// (mom_record) when a soft-filter false-positive matters.
//
// Returns true when the turn should be silently dropped (no memory
// row, no filter_audit increment).
func isNoise(t softTurn) bool {
	// Strip thinking blocks and surrounding whitespace before
	// classification — a turn that is *only* a thinking block has
	// nothing left after stripping.
	stripped := strings.TrimSpace(thinkingBlockRE.ReplaceAllString(t.Text, ""))

	// Tool-only assistant turn (no text content): noise. The
	// tool-call metadata still lands on Logbook's projection, but a
	// memory row would have no useful content.
	if t.Role == "assistant" && stripped == "" && t.ToolCount > 0 {
		return true
	}

	// Pure code-write turn: a single tool call that writes a file,
	// no narrative. Code lives in the repo, not in MOM.
	if t.Role == "assistant" && stripped == "" &&
		t.ToolCount == 1 && t.CodebaseWriteToolCount == 1 {
		return true
	}

	// Inner-monologue-only: thinking block with no prose. After the
	// strip above, the remaining text is empty.
	if stripped == "" {
		return true
	}

	// Bare acknowledgements: short turn whose normalised content is
	// in the ack set.
	clean := strings.ToLower(strings.TrimRight(stripped, "!.?"))
	clean = strings.TrimSpace(clean)
	if _, ok := ackPhrases[clean]; ok {
		return true
	}

	// Length threshold after stop-word removal. A turn with fewer
	// than minMeaningfulWords non-stop-words is too thin.
	if meaningfulWordCount(stripped) < minMeaningfulWords {
		return true
	}

	return false
}

// stopwordsForSoftFilter is a small embedded list. The drafter
// package already loads a fuller list for RAKE/keywording; the soft
// filter uses its own narrow list because RAKE's stopwords vary by
// language pack and we want consistent behaviour for noise
// classification regardless of which language pack is loaded.
var stopwordsForSoftFilter = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "is": {}, "it": {}, "to": {},
	"of": {}, "in": {}, "on": {}, "at": {}, "and": {}, "or": {},
	"but": {}, "for": {}, "by": {}, "with": {}, "this": {}, "that": {},
	"i": {}, "you": {}, "he": {}, "she": {}, "they": {}, "we": {},
	"be": {}, "do": {}, "have": {}, "will": {}, "can": {}, "would": {},
	"so": {}, "if": {}, "as": {}, "are": {}, "was": {}, "were": {},
}

// meaningfulWordCount counts whitespace-split tokens after dropping
// stopwords and punctuation-only tokens. Cheap stand-in for "does
// this turn carry signal?"
func meaningfulWordCount(s string) int {
	count := 0
	for _, w := range strings.Fields(s) {
		w = strings.ToLower(strings.Trim(w, ".,!?;:()[]{}\"'`"))
		if w == "" {
			continue
		}
		if _, isStop := stopwordsForSoftFilter[w]; isStop {
			continue
		}
		count++
	}
	return count
}
