package librarian

import (
	"strings"
	"unicode"
)

// NormalizeTagName produces the canonical form of a tag name per
// ADR 0010 (T2): lowercase, trim, collapse runs of non-alphanumeric
// (Unicode-aware) to a single hyphen, trim hyphen ends. Unicode
// letters/digits are preserved ("メモリ"); ASCII punctuation is
// collapsed ("v0.30" → "v0-30").
//
// All write entry points (mom_record, Drafter capture, Wrap-up) call
// this helper. RenameTag and MergeTags themselves stay case-sensitive
// — normalization is a higher-level convention layered above the
// storage primitive.
func NormalizeTagName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}
