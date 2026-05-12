package watcher

import "testing"

// Test fixtures: representative Codex CLI transcript JSONL lines.
//
// Codex writes one JSON object per line with a top-level envelope:
//
//	{ "timestamp": "...", "type": "session_meta"|"turn_context"|"response_item"|"event_msg", "payload": {...} }
//
// Only `response_item` lines produce Turns. Inside response_item, the
// payload's own `type` determines what kind of turn (message, function_call,
// custom_tool_call, etc.).
const codexAssistantTextTurn = `{"timestamp":"2026-05-09T20:53:08.920Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello, world."}],"phase":"commentary"}}`

const codexUserMultiBlockTurn = `{"timestamp":"2026-05-09T20:53:00.659Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first block"},{"type":"input_text","text":"second block"}]}}`

func TestCodexAdapter_ExtractTurn_UserMultiBlockText(t *testing.T) {
	a := NewCodexAdapter()
	turn, ok := a.ExtractTurn([]byte(codexUserMultiBlockTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true for user multi-block turn")
	}
	if turn.Role != "user" {
		t.Errorf("Role = %q, want user", turn.Role)
	}
	want := "first block\nsecond block"
	if turn.Text != want {
		t.Errorf("Text = %q, want %q", turn.Text, want)
	}
}

const codexFunctionCallTurn = `{"timestamp":"2026-05-09T20:53:08.920Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls -la\",\"workdir\":\"/tmp\"}","call_id":"call_abc"}}`

func TestCodexAdapter_ExtractTurn_FunctionCall(t *testing.T) {
	a := NewCodexAdapter()
	turn, ok := a.ExtractTurn([]byte(codexFunctionCallTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true for function_call")
	}
	if turn.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", turn.Role)
	}
	if turn.Text != "" {
		t.Errorf("Text should be empty for function_call, got %q", turn.Text)
	}
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1", len(turn.ToolCalls))
	}
	tc := turn.ToolCalls[0]
	if tc.Name != "exec_command" {
		t.Errorf("ToolCall.Name = %q, want exec_command", tc.Name)
	}
	if tc.Input["cmd"] != "ls -la" {
		t.Errorf("ToolCall.Input[cmd] = %v, want ls -la", tc.Input["cmd"])
	}
	if tc.Input["workdir"] != "/tmp" {
		t.Errorf("ToolCall.Input[workdir] = %v, want /tmp", tc.Input["workdir"])
	}
}

const codexCustomToolCallTurn = `{"timestamp":"2026-05-09T20:54:35.460Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_xyz","name":"apply_patch","input":"*** Begin Patch\n*** Add File: foo.txt\n+hello\n*** End Patch\n"}}`

func TestCodexAdapter_ExtractTurn_CustomToolCall(t *testing.T) {
	a := NewCodexAdapter()
	turn, ok := a.ExtractTurn([]byte(codexCustomToolCallTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true for custom_tool_call")
	}
	if turn.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", turn.Role)
	}
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1", len(turn.ToolCalls))
	}
	tc := turn.ToolCalls[0]
	if tc.Name != "apply_patch" {
		t.Errorf("ToolCall.Name = %q, want apply_patch", tc.Name)
	}
	// custom_tool_call.input is a raw string, not JSON. Stash it under a
	// stable key so Drafter / Logbook can still inspect it.
	if _, ok := tc.Input["raw"]; !ok {
		t.Errorf("expected ToolCall.Input[\"raw\"] to be present, got %v", tc.Input)
	}
}

func TestCodexAdapter_ExtractTurn_SkipsNonTurnLines(t *testing.T) {
	a := NewCodexAdapter()

	cases := []struct {
		name string
		line string
	}{
		{"session_meta", `{"timestamp":"2026-05-09T20:53:00.657Z","type":"session_meta","payload":{"id":"019e0e83"}}`},
		{"turn_context", `{"timestamp":"2026-05-09T20:53:00.659Z","type":"turn_context","payload":{"turn_id":"x","model":"gpt-5.5"}}`},
		{"event_msg/token_count", `{"timestamp":"2026-05-09T20:53:01.055Z","type":"event_msg","payload":{"type":"token_count","info":null}}`},
		{"response_item/reasoning", `{"timestamp":"2026-05-09T20:53:01.055Z","type":"response_item","payload":{"type":"reasoning","summary":[]}}`},
		{"response_item/function_call_output", `{"timestamp":"2026-05-09T20:53:01.055Z","type":"response_item","payload":{"type":"function_call_output","call_id":"x","output":"y"}}`},
		{"response_item/custom_tool_call_output", `{"timestamp":"2026-05-09T20:53:01.055Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x","output":"y"}}`},
		{"empty line", ``},
		{"malformed json", `{not json}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			turn, ok := a.ExtractTurn([]byte(c.line), "s-fallback")
			if ok {
				t.Errorf("%s should be skipped, got turn: %+v", c.name, turn)
			}
		})
	}
}

func TestCodexAdapter_ExtractTurn_ParsesTimestampAndUsesSessionFallback(t *testing.T) {
	a := NewCodexAdapter()
	turn, ok := a.ExtractTurn([]byte(codexAssistantTextTurn), "session-from-filename")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if turn.SessionID != "session-from-filename" {
		t.Errorf("SessionID = %q, want session-from-filename (Codex lines don't carry session id, adapter must use fallback)", turn.SessionID)
	}
	wantTS := "2026-05-09T20:53:08.92Z"
	if turn.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z") != wantTS &&
		turn.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z") != wantTS {
		got := turn.Timestamp.UTC().Format("2006-01-02T15:04:05.999Z")
		if got != wantTS {
			t.Errorf("Timestamp = %s, want %s", got, wantTS)
		}
	}
}

func TestCodexAdapter_ExtractTurn_AssistantText(t *testing.T) {
	a := NewCodexAdapter()
	turn, ok := a.ExtractTurn([]byte(codexAssistantTextTurn), "s-fallback")
	if !ok {
		t.Fatal("expected ok=true for assistant text turn")
	}
	if turn.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", turn.Role)
	}
	if turn.Text != "Hello, world." {
		t.Errorf("Text = %q, want %q", turn.Text, "Hello, world.")
	}
	if turn.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", turn.Provider)
	}
	if turn.Harness != "codex" {
		t.Errorf("Harness = %q, want codex", turn.Harness)
	}
}
