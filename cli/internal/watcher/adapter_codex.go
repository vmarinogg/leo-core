package watcher

import (
	"encoding/json"
	"strings"
	"time"
)

// CodexAdapter parses Codex CLI JSONL transcript lines.
//
// Each line is a JSON envelope:
//
//	{ "timestamp": "...", "type": "...", "payload": {...} }
//
// Only `response_item` lines produce Turns. Inside, payload.type selects
// the shape — `message` for user/assistant text, `function_call` and
// `custom_tool_call` for assistant tool invocations. Everything else is
// dropped.
type CodexAdapter struct{}

// NewCodexAdapter returns a new CodexAdapter.
func NewCodexAdapter() *CodexAdapter { return &CodexAdapter{} }

func (a *CodexAdapter) Name() string { return "codex" }

type codexEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexMessagePayload struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []codexContentBlock `json:"content"`
}

type codexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ExtractTurn implements Adapter.
func (a *CodexAdapter) ExtractTurn(line []byte, sessionID string) (Turn, bool) {
	line = trimLine(line)
	if len(line) == 0 {
		return Turn{}, false
	}
	var env codexEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Turn{}, false
	}
	if env.Type != "response_item" {
		return Turn{}, false
	}

	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(env.Payload, &head); err != nil {
		return Turn{}, false
	}

	ts := parseCodexTimestamp(env.Timestamp)

	var (
		turn Turn
		ok   bool
	)
	switch head.Type {
	case "message":
		turn, ok = a.extractMessage(env.Payload, sessionID)
	case "function_call":
		turn, ok = a.extractFunctionCall(env.Payload, sessionID)
	case "custom_tool_call":
		turn, ok = a.extractCustomToolCall(env.Payload, sessionID)
	}
	if !ok {
		return Turn{}, false
	}
	if !ts.IsZero() {
		turn.Timestamp = ts
	} else {
		turn.Timestamp = time.Now().UTC()
	}
	return turn, true
}

func parseCodexTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

type codexFunctionCallPayload struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
}

func (a *CodexAdapter) extractFunctionCall(payload []byte, sessionID string) (Turn, bool) {
	var fc codexFunctionCallPayload
	if err := json.Unmarshal(payload, &fc); err != nil || fc.Name == "" {
		return Turn{}, false
	}
	input := map[string]any{}
	if fc.Arguments != "" {
		_ = json.Unmarshal([]byte(fc.Arguments), &input)
	}
	category, safeName := CategorizeObservedToolCall(fc.Name, input)
	return Turn{
		SessionID: sessionID,
		Role:      "assistant",
		Provider:  "openai",
		Harness:   "codex",
		ToolCalls: []ToolCall{{
			Name:     fc.Name,
			SafeName: safeName,
			Input:    input,
			Category: category,
		}},
	}, true
}

type codexCustomToolCallPayload struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	CallID string `json:"call_id"`
}

func (a *CodexAdapter) extractCustomToolCall(payload []byte, sessionID string) (Turn, bool) {
	var ct codexCustomToolCallPayload
	if err := json.Unmarshal(payload, &ct); err != nil || ct.Name == "" {
		return Turn{}, false
	}
	// custom_tool_call.input is a raw string (e.g. a patch body), not JSON.
	// Stash it under "raw" so downstream consumers have a stable key.
	input := map[string]any{"raw": ct.Input}
	category, safeName := CategorizeObservedToolCall(ct.Name, input)
	return Turn{
		SessionID: sessionID,
		Role:      "assistant",
		Provider:  "openai",
		Harness:   "codex",
		ToolCalls: []ToolCall{{
			Name:     ct.Name,
			SafeName: safeName,
			Input:    input,
			Category: category,
		}},
	}, true
}

func (a *CodexAdapter) extractMessage(payload []byte, sessionID string) (Turn, bool) {
	var m codexMessagePayload
	if err := json.Unmarshal(payload, &m); err != nil {
		return Turn{}, false
	}
	if m.Role != "user" && m.Role != "assistant" {
		return Turn{}, false
	}
	var parts []string
	for _, b := range m.Content {
		if (b.Type == "input_text" || b.Type == "output_text") && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		return Turn{}, false
	}
	return Turn{
		SessionID: sessionID,
		Role:      m.Role,
		Text:      text,
		Provider:  "openai",
		Harness:   "codex",
	}, true
}
