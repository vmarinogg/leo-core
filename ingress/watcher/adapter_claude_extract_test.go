package watcher

import (
	"reflect"
	"testing"
)

// Test fixtures: representative Claude Code transcript JSONL lines.

// Real transcripts are single-line JSON per record (JSONL). Fixtures
// follow that shape so they round-trip through the watcher's
// line-by-line reader correctly.
const claudeUserTurn = `{"type":"user","sessionId":"s-test","timestamp":"2026-05-05T12:00:00.000Z","isSidechain":false,"message":{"role":"user","content":"deploy postgres canary"}}`

const claudeAssistantTextTurn = `{"type":"assistant","sessionId":"s-test","timestamp":"2026-05-05T12:00:01.000Z","isSidechain":false,"message":{"id":"msg_abc","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Sure, deploying now."}],"stop_reason":"end_turn","usage":{"input_tokens":1234,"output_tokens":56,"cache_creation_input_tokens":100,"cache_read_input_tokens":200}}}`

const claudeAssistantToolUseTurn = `{"type":"assistant","sessionId":"s-test","timestamp":"2026-05-05T12:00:02.000Z","isSidechain":false,"message":{"id":"msg_def","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"Reading the file."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/Users/x/secrets.env"}},{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"echo AKIA1234567890ABCDEF"}}],"stop_reason":"tool_use","usage":{"input_tokens":500,"output_tokens":80}}}`

const claudeSidechainTurn = `{"type":"assistant","sessionId":"s-test","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent"}]}}`

const claudeToolResultTurn = `{"type":"user","sessionId":"s-test","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file content"}]}}`

func TestClaudeAdapter_ExtractTurn_UserText(t *testing.T) {
	a := NewClaudeAdapter()
	turn, ok := a.ExtractTurn([]byte(claudeUserTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true for user turn")
	}
	if turn.SessionID != "s-test" {
		t.Errorf("SessionID = %q, want s-test (from line)", turn.SessionID)
	}
	if turn.Role != "user" {
		t.Errorf("Role = %q, want user", turn.Role)
	}
	if turn.Text != "deploy postgres canary" {
		t.Errorf("Text = %q", turn.Text)
	}
	if len(turn.ToolCalls) != 0 {
		t.Errorf("user turn should have no tool calls, got %v", turn.ToolCalls)
	}
	if turn.Usage != nil {
		t.Errorf("user turn should have no usage, got %+v", turn.Usage)
	}
	if turn.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", turn.Provider)
	}
}

func TestClaudeAdapter_ExtractTurn_AssistantTextWithUsage(t *testing.T) {
	a := NewClaudeAdapter()
	turn, ok := a.ExtractTurn([]byte(claudeAssistantTextTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if turn.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", turn.Role)
	}
	if turn.Text != "Sure, deploying now." {
		t.Errorf("Text = %q", turn.Text)
	}
	if turn.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q", turn.Model)
	}
	if turn.Usage == nil {
		t.Fatal("Usage should be non-nil for assistant turn with usage block")
	}
	if turn.Usage.InputTokens != 1234 {
		t.Errorf("InputTokens = %d, want 1234", turn.Usage.InputTokens)
	}
	if turn.Usage.OutputTokens != 56 {
		t.Errorf("OutputTokens = %d, want 56", turn.Usage.OutputTokens)
	}
	if turn.Usage.CacheReadTokens != 200 {
		t.Errorf("CacheReadTokens = %d, want 200", turn.Usage.CacheReadTokens)
	}
	if turn.Usage.CacheWriteTokens != 100 {
		t.Errorf("CacheWriteTokens = %d, want 100", turn.Usage.CacheWriteTokens)
	}
	if turn.Usage.StopReason != "end_turn" {
		t.Errorf("StopReason = %q", turn.Usage.StopReason)
	}
}

func TestClaudeAdapter_ExtractTurn_AssistantWithToolCalls(t *testing.T) {
	a := NewClaudeAdapter()
	turn, ok := a.ExtractTurn([]byte(claudeAssistantToolUseTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if turn.Text != "Reading the file." {
		t.Errorf("Text = %q (text-typed blocks should be concatenated, tool_use blocks excluded)", turn.Text)
	}
	if len(turn.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(turn.ToolCalls))
	}
	read := turn.ToolCalls[0]
	if read.Name != "Read" {
		t.Errorf("ToolCalls[0].Name = %q, want Read", read.Name)
	}
	if read.Category != "codebase_read" {
		t.Errorf("ToolCalls[0].Category = %q, want codebase_read", read.Category)
	}
	if got, _ := read.Input["file_path"].(string); got != "/Users/x/secrets.env" {
		t.Errorf("Read.Input.file_path = %q (full input must be preserved for Drafter's filter)", got)
	}
	bash := turn.ToolCalls[1]
	if bash.Name != "Bash" {
		t.Errorf("ToolCalls[1].Name = %q, want Bash", bash.Name)
	}
	if bash.Category != "system" {
		t.Errorf("ToolCalls[1].Category = %q, want system", bash.Category)
	}
	if got, _ := bash.Input["command"].(string); got != "echo AKIA1234567890ABCDEF" {
		t.Errorf("Bash.Input.command should preserve raw text; got %q", got)
	}
}

func TestClaudeAdapter_ExtractTurn_SkipsSidechain(t *testing.T) {
	a := NewClaudeAdapter()
	if _, ok := a.ExtractTurn([]byte(claudeSidechainTurn), "s"); ok {
		t.Fatal("sidechain turn should be skipped")
	}
}

func TestClaudeAdapter_ExtractTurn_SkipsToolResultOnlyTurn(t *testing.T) {
	a := NewClaudeAdapter()
	turn, ok := a.ExtractTurn([]byte(claudeToolResultTurn), "s")
	// Tool-result-only user turns produce no text and no tool_calls;
	// they should be skipped.
	if ok && turn.Text == "" && len(turn.ToolCalls) == 0 {
		t.Fatal("tool-result-only turn returned ok=true with empty payload — should be skipped")
	}
}

func TestClaudeAdapter_ExtractTurn_FallsBackToSessionIDArg(t *testing.T) {
	// A line without a top-level sessionId field should fall back to
	// the watcher-supplied sessionID (derived from filename).
	line := `{"type":"user","message":{"role":"user","content":"hi"}}`
	a := NewClaudeAdapter()
	turn, ok := a.ExtractTurn([]byte(line), "s-from-filename")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if turn.SessionID != "s-from-filename" {
		t.Errorf("SessionID = %q, want fallback s-from-filename", turn.SessionID)
	}
}

func TestClaudeAdapter_ExtractTurn_RejectsMalformedJSON(t *testing.T) {
	a := NewClaudeAdapter()
	if _, ok := a.ExtractTurn([]byte("not valid json"), "s"); ok {
		t.Fatal("malformed line should return ok=false")
	}
}

// Sanity: the rich extraction does not depend on legacy ParseLine
// semantics — make sure ExtractTurn works on the same fixtures
// without using the recorder.RawEntry type at all.
func TestClaudeAdapter_ExtractTurn_DoesNotPolluteRawEntry(t *testing.T) {
	a := NewClaudeAdapter()
	turn, _ := a.ExtractTurn([]byte(claudeAssistantTextTurn), "s")
	// Reflect-based smoke check: the type returned is watcher.Turn,
	// not recorder.RawEntry.
	if reflect.TypeOf(turn).Name() != "Turn" {
		t.Errorf("ExtractTurn returned %v, want watcher.Turn", reflect.TypeOf(turn))
	}
}
