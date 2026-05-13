package watcher

import "time"

// Turn is the per-turn structured payload emitted by the watcher's
// adapters. It carries everything Drafter needs to make filter
// decisions (raw text, tool inputs) AND everything Logbook needs to
// produce a privacy-safe metadata projection (role, tool categories,
// usage, model, provider).
//
// The full Turn rides on the Herald bus inside `turn.observed` events.
// It is NEVER persisted in raw form. Drafter persists a redacted
// memory through Librarian; Logbook persists a metadata projection.
// See PRD 0003 + ADR 0014 for the privacy contract.
type Turn struct {
	SessionID string
	Timestamp time.Time
	Role      string // "user" | "assistant"
	Text      string
	ToolCalls []ToolCall
	Usage     *Usage

	// Three orthogonal identity fields, each answering a different
	// question. Any may be empty when the source transcript does not
	// surface the data.
	Model    string // "the model": e.g. "claude-sonnet-4-6", "gpt-4o"
	Provider string // "provided by whom": model vendor — "anthropic", "openai", …
	Harness  string // "used in which client": "claude-code", "codex", "pi"

	// ProjectId carries the resolved project identity (ADR 0016).
	// Empty means "unknown" — the resolver found no .mom-project.yaml.
	ProjectId string
}

// ToolCall is one tool invocation observed in an assistant turn.
// `Input` carries the raw tool arguments (file paths, shell commands,
// etc.) for Drafter's filter pipeline. `Category` and SafeName are
// pre-computed by the adapter so Logbook can persist a privacy-safe
// metadata projection without inspecting raw inputs.
type ToolCall struct {
	Name     string
	SafeName string
	Input    map[string]any
	Category string // "mom_memory" | "mom_cli" | "codebase_read" | "codebase_write" | "system"
}

// Usage carries token-accounting numbers for a single turn. Optional —
// not every harness surfaces it (e.g. user turns rarely have usage).
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	TotalTokens      int
	CostUSD          float64
	StopReason       string
}

// ToPayload renders a Turn into the map[string]any shape carried by
// Herald's turn.observed event. Drafter and Logbook both read these
// keys; the map convention is documented here so subscribers don't
// reinvent extraction.
//
// Keys: "role", "text", "tool_calls" ([]map with name/input/category),
// "usage" (map of token counts), "model", "provider", "harness",
// "project_id".
func (t Turn) ToPayload() map[string]any {
	out := map[string]any{
		"role": t.Role,
	}
	if t.Text != "" {
		out["text"] = t.Text
	}
	if len(t.ToolCalls) > 0 {
		tcs := make([]map[string]any, 0, len(t.ToolCalls))
		for _, tc := range t.ToolCalls {
			m := map[string]any{
				"name":     tc.Name,
				"category": tc.Category,
			}
			if tc.SafeName != "" {
				m["safe_name"] = tc.SafeName
			}
			if tc.Input != nil {
				m["input"] = tc.Input
			}
			tcs = append(tcs, m)
		}
		out["tool_calls"] = tcs
	}
	if t.Usage != nil {
		out["usage"] = map[string]any{
			"input_tokens":       t.Usage.InputTokens,
			"output_tokens":      t.Usage.OutputTokens,
			"cache_read_tokens":  t.Usage.CacheReadTokens,
			"cache_write_tokens": t.Usage.CacheWriteTokens,
			"total_tokens":       t.Usage.TotalTokens,
			"cost_usd":           t.Usage.CostUSD,
			"stop_reason":        t.Usage.StopReason,
		}
	}
	if t.Model != "" {
		out["model"] = t.Model
	}
	if t.Provider != "" {
		out["provider"] = t.Provider
	}
	if t.Harness != "" {
		out["harness"] = t.Harness
	}
	if t.ProjectId != "" {
		out["project_id"] = t.ProjectId
	}
	return out
}
