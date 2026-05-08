package librarian_test

import (
	"testing"

	"github.com/momhq/mom/cli/internal/librarian"
)

func TestNormalizeTagName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"recall", "recall"},
		{"Recall", "recall"},
		{"  recall  ", "recall"},
		{"v0.30", "v0-30"},
		{"foo bar", "foo-bar"},
		{"foo___bar", "foo-bar"},
		{"---foo---", "foo"},
		{"FOO bar BAZ", "foo-bar-baz"},
		// Unicode letters and digits are preserved.
		{"メモリ", "メモリ"},
		{"日本語tag", "日本語tag"},
		{"emoji-✨-tag", "emoji-tag"}, // ✨ is symbol, not letter/digit → collapsed
		// Empty / whitespace input.
		{"", ""},
		{"   ", ""},
		{"!!!", ""},
		// Mixed punctuation collapses to single hyphens.
		{"a!@#$%b", "a-b"},
		{"a---b", "a-b"},
	}
	for _, tc := range cases {
		got := librarian.NormalizeTagName(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeTagName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
