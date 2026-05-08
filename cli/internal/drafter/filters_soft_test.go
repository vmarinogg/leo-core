package drafter

import "testing"

func TestIsNoise_ShortAck(t *testing.T) {
	cases := []string{
		"ok",
		"thanks",
		"got it",
		"sounds good",
		"sure",
		"yes",
		"no",
		"👍",
		"ok!",
		"thx",
	}
	for _, c := range cases {
		if !isNoise(softTurn{Role: "user", Text: c}) {
			t.Errorf("ack %q should be classified as noise", c)
		}
	}
}

func TestIsNoise_TooShort(t *testing.T) {
	// Below the minimum-length threshold (after trimming + stop-word
	// removal) the turn is noise regardless of content.
	cases := []string{
		"hi",
		"a b",
		"the the",
	}
	for _, c := range cases {
		if !isNoise(softTurn{Role: "user", Text: c}) {
			t.Errorf("short text %q should be noise", c)
		}
	}
}

func TestIsNoise_KeepsRealTurn(t *testing.T) {
	// Substantive turns must NOT be classified as noise.
	cases := []string{
		"deploy postgres canary, set the connection pool to 50",
		"I was confused why the migration ran twice — checking the locks now",
		"The build is failing because gofmt rewrote the imports",
	}
	for _, c := range cases {
		if isNoise(softTurn{Role: "user", Text: c}) {
			t.Errorf("substantive text should NOT be noise: %q", c)
		}
	}
}

func TestIsNoise_ToolOnlyAssistantTurn(t *testing.T) {
	// Assistant turns that contain only tool calls (no text) are
	// noise — they're action-only and contribute nothing to the
	// memory. Drafter still observes the tool_call categories via
	// Logbook; they're just not memory-worthy.
	turn := softTurn{
		Role:      "assistant",
		Text:      "",
		ToolCount: 2,
	}
	if !isNoise(turn) {
		t.Errorf("tool-only assistant turn should be noise")
	}
}

func TestIsNoise_PureCodeWriteTurn(t *testing.T) {
	// Single tool call that writes a file, no text. v1 logbook
	// "Code being typed" rule. Code lives in the repo, not in MOM.
	turn := softTurn{
		Role:                  "assistant",
		Text:                  "",
		ToolCount:             1,
		CodebaseWriteToolCount: 1,
	}
	if !isNoise(turn) {
		t.Errorf("pure code-write turn should be noise")
	}
}

func TestIsNoise_InnerMonologueOnly(t *testing.T) {
	// Inner-monologue marker (Claude's <thinking>...</thinking>) with
	// no surrounding text is noise. Per ADR 0014.
	turn := softTurn{
		Role: "assistant",
		Text: "<thinking>I should consider both branches before deciding.</thinking>",
	}
	if !isNoise(turn) {
		t.Errorf("thinking-only turn should be noise")
	}
}

func TestIsNoise_ThinkingPlusRealTextIsNotNoise(t *testing.T) {
	// A turn with a thinking block but ALSO real prose is not noise —
	// the prose is the substance.
	turn := softTurn{
		Role: "assistant",
		Text: "<thinking>weighing options</thinking>I'll deploy postgres canary first.",
	}
	if isNoise(turn) {
		t.Errorf("turn with substantive text alongside thinking should NOT be noise")
	}
}
