package watcher

import (
	"encoding/json"
	"strings"
	"time"
)

// ClaudeAdapter parses Claude Code JSONL transcript lines.
// Claude Code writes one JSON object per line with the schema:
//
//	{ type, message: { role, content, model, usage }, timestamp, sessionId, uuid, cwd, gitBranch, isSidechain }
//
// We keep only type=="user" and type=="assistant" entries; everything else
// (tool_use, tool_result, system, hook_progress) is dropped.
type ClaudeAdapter struct{}

// NewClaudeAdapter returns a new ClaudeAdapter.
func NewClaudeAdapter() *ClaudeAdapter {
	return &ClaudeAdapter{}
}

func (a *ClaudeAdapter) Name() string { return "claude" }

// claudeTranscriptLine is the minimal subset of a Claude Code JSONL line
// that the adapter needs to inspect.
type claudeTranscriptLine struct {
	Type        string        `json:"type"`
	Message     claudeMessage `json:"message"`
	Timestamp   string        `json:"timestamp"`
	SessionID   string        `json:"sessionId"`
	IsSidechain bool          `json:"isSidechain"`
}

type claudeMessage struct {
	Role       string       `json:"role"`
	Model      string       `json:"model,omitempty"`
	Content    any          `json:"content"` // string or []claudeContentItem
	Usage      *claudeUsage `json:"usage,omitempty"`
	StopReason string       `json:"stop_reason,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// ExtractTurn implements Adapter. Returns the rich per-turn
// shape Drafter and Logbook consume from `turn.observed` events. The
// raw text and tool inputs ride on the bus only — Drafter applies
// the redaction pipeline before persisting; Logbook strips them
// before the metadata projection lands in op_events.
func (a *ClaudeAdapter) ExtractTurn(line []byte, sessionID string) (Turn, bool) {
	line = trimLine(line)
	if len(line) == 0 {
		return Turn{}, false
	}
	var tl claudeTranscriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return Turn{}, false
	}
	if tl.Type != "user" && tl.Type != "assistant" {
		return Turn{}, false
	}
	if tl.IsSidechain {
		return Turn{}, false
	}

	turn := Turn{
		SessionID: tl.SessionID,
		Role:      tl.Type,
		Model:     tl.Message.Model,
		Provider:  "anthropic",
		Harness:   "claude-code",
	}
	if turn.SessionID == "" {
		turn.SessionID = sessionID
	}

	// Timestamp: prefer line's, fall back to now.
	if tl.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, tl.Timestamp); err == nil {
			turn.Timestamp = t
		} else if t, err := time.Parse(time.RFC3339, tl.Timestamp); err == nil {
			turn.Timestamp = t
		}
	}
	if turn.Timestamp.IsZero() {
		turn.Timestamp = time.Now().UTC()
	}

	// Text + tool calls: walk the structured content blocks.
	turn.Text, turn.ToolCalls = walkClaudeContent(tl.Message.Content)

	// Usage: lift from message.usage if present.
	if tl.Message.Usage != nil {
		u := &Usage{
			InputTokens:      tl.Message.Usage.InputTokens,
			OutputTokens:     tl.Message.Usage.OutputTokens,
			CacheReadTokens:  tl.Message.Usage.CacheReadInputTokens,
			CacheWriteTokens: tl.Message.Usage.CacheCreationInputTokens,
			StopReason:       tl.Message.StopReason,
		}
		u.TotalTokens = u.InputTokens + u.OutputTokens
		turn.Usage = u
	}

	// Drop turns with no text and no tool calls (e.g. tool_result-only
	// blocks). They carry no signal for either Drafter or Logbook.
	if turn.Text == "" && len(turn.ToolCalls) == 0 {
		return Turn{}, false
	}

	return turn, true
}

// walkClaudeContent traverses message.content (string or array of
// blocks) and returns:
//   - the concatenated text from text-typed blocks
//   - the tool calls extracted from tool_use-typed blocks (with
//     pre-computed Category)
//
// Other block types (tool_result, image, etc.) are ignored.
func walkClaudeContent(content any) (string, []ToolCall) {
	if content == nil {
		return "", nil
	}
	if s, ok := content.(string); ok {
		return strings.TrimSpace(s), nil
	}
	items, ok := content.([]any)
	if !ok {
		return "", nil
	}
	var (
		textParts []string
		tcs       []ToolCall
	)
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["type"].(string)
		switch t {
		case "text":
			if text, _ := m["text"].(string); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			input, _ := m["input"].(map[string]any)
			category, safeName := CategorizeObservedToolCall(name, input)
			tcs = append(tcs, ToolCall{
				Name:     name,
				SafeName: safeName,
				Input:    input,
				Category: category,
			})
		}
	}
	return strings.Join(textParts, "\n"), tcs
}

// trimLine removes leading/trailing whitespace from a byte slice.
func trimLine(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
