package watcher

import (
	"testing"
	"time"
)

const piUserMessage = `{"type":"message","timestamp":"2026-05-05T12:00:00Z","message":{"role":"user","content":"deploy postgres canary"}}`

const piAssistantMessage = `{"type":"message","timestamp":"2026-05-05T12:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Sure, deploying now."},{"type":"thinking","text":"User wants postgres."}]}}`

const piToolUseLine = `{"type":"tool_use","timestamp":"2026-05-05T12:00:02Z","name":"Bash","input":{"command":"ls"}}`

func TestPiAdapter_ExtractTurn_UserMessage(t *testing.T) {
	a := NewPiAdapter()
	turn, ok := a.ExtractTurn([]byte(piUserMessage), "s-pi")
	if !ok {
		t.Fatal("expected ok=true for user message")
	}
	if turn.SessionID != "s-pi" {
		t.Errorf("SessionID = %q, want s-pi", turn.SessionID)
	}
	if turn.Role != "user" {
		t.Errorf("Role = %q, want user", turn.Role)
	}
	if turn.Text != "deploy postgres canary" {
		t.Errorf("Text = %q", turn.Text)
	}
	if turn.Harness != "pi" {
		t.Errorf("Harness = %q, want pi", turn.Harness)
	}
}

func TestPiAdapter_ExtractTurn_AssistantTextOnly(t *testing.T) {
	a := NewPiAdapter()
	turn, ok := a.ExtractTurn([]byte(piAssistantMessage), "s-pi")
	if !ok {
		t.Fatal("expected ok=true for assistant message")
	}
	if turn.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", turn.Role)
	}
	if turn.Text != "Sure, deploying now." {
		t.Errorf("Text = %q (thinking blocks must be stripped)", turn.Text)
	}
}

func TestPiAdapter_ExtractTurn_DropsToolUseLine(t *testing.T) {
	a := NewPiAdapter()
	if _, ok := a.ExtractTurn([]byte(piToolUseLine), "s-pi"); ok {
		t.Error("expected ok=false for non-message line")
	}
}

func TestPiAdapter_ExtractTurn_RejectsMalformedJSON(t *testing.T) {
	a := NewPiAdapter()
	if _, ok := a.ExtractTurn([]byte(`not-json`), "s-pi"); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

// TestPiAdapter_ExtractTurn_PrefersLineTimestamp locks the catch-up
// correctness rule: when the transcript line carries a timestamp,
// the resulting Turn carries that exact value, not wall-clock now.
// Drafter persists it as memories.created_at, so a regression here
// would corrupt memory ordering on every startup catch-up read.
func TestPiAdapter_ExtractTurn_PrefersLineTimestamp(t *testing.T) {
	a := NewPiAdapter()
	turn, ok := a.ExtractTurn([]byte(piUserMessage), "s-pi")
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := "2026-05-05T12:00:00Z"
	if got := turn.Timestamp.UTC().Format(time.RFC3339); got != want {
		t.Errorf("Timestamp = %q, want %q (line timestamp must win over time.Now())", got, want)
	}
}
