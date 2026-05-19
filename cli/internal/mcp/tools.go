package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/momhq/mom/cli/internal/finder"
	"github.com/momhq/mom/cli/internal/librarian"
)

// toolDef describes one MCP tool for the tools/list response.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolResult is the content item returned in tools/call responses.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolCallResult wraps the content list returned by a tool call.
type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// allTools returns the static v0.30 tool catalogue.
func allTools() []toolDef {
	return []toolDef{
		{
			Name:        "mom_status",
			Description: "Returns MOM's operating protocol and central v0.30 vault status. Call this at the start of every session.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "mom_recall",
			Description: "Search persistent memory with a natural-language query.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search query"},
				},
			},
		},
		{
			Name:        "mom_get",
			Description: "Retrieve a single memory by ID from the central vault.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"id"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Memory ID"},
				},
			},
		},
		{
			Name:        "mom_landmarks",
			Description: "List landmark memories from the central vault sorted by centrality_score descending.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Maximum results (default 20)"},
				},
			},
		},
		{
			Name:        "mom_record",
			Description: "Fallback explicit-write path: intentionally save a memory mid-session when CLI is unavailable. Bypasses Drafter's content filters and stamps trigger_event='record', source_type='manual-draft'. Required: content. Optional: session_id only when it is a real harness session ID; never invent one. Optional: summary, tags, actor.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"content"},
				"properties": map[string]any{
					"session_id": map[string]any{"type": "string", "description": "Real harness session ID only. Omit when unavailable; MOM resolves known harness env vars or rejects."},
					"summary":    map[string]any{"type": "string", "description": "One-line summary"},
					"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tag names (will be normalised; empty after normalisation rejects the request)"},
					"content":    map[string]any{"type": "object", "description": "Memory content (must include $.text for FTS)"},
					"actor":      map[string]any{"type": "string", "description": "Calling agent (claude-code, codex, …); defaults to 'mcp'"},
				},
			},
		},
	}
}

// handleToolsList returns the static tool catalogue.
func (s *Server) handleToolsList() (any, *rpcError) {
	tools := allTools()
	out := make([]any, len(tools))
	for i, t := range tools {
		out[i] = t
	}
	return map[string]any{"tools": out}, nil
}

// handleToolsCall dispatches a tools/call request.
func (s *Server) handleToolsCall(params json.RawMessage) (any, *rpcError) {
	var req struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: errCodeInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}

	var (
		result toolCallResult
		err    error
	)

	switch req.Name {
	case "mom_status":
		result, err = s.toolMomStatus()
	case "mom_recall":
		result, err = s.toolMomRecall(req.Arguments)
	case "mom_get":
		result, err = s.toolMomGet(req.Arguments)
	case "mom_landmarks":
		result, err = s.toolMomLandmarks(req.Arguments)
	case "mom_record":
		result, err = s.toolMomRecord(req.Arguments)
	default:
		return nil, &rpcError{Code: errCodeMethodNotFound, Message: "unknown tool: " + req.Name}
	}

	if err != nil {
		return toolCallResult{
			IsError: true,
			Content: []toolContent{{Type: "text", Text: err.Error()}},
		}, nil
	}
	return result, nil
}

// --- Tool implementations ---

func (s *Server) requireLibrarian() (*librarian.Librarian, error) {
	if s.lib == nil {
		if s.openErr != nil {
			return nil, s.openErr
		}
		return nil, errors.New("central vault is not open")
	}
	return s.lib, nil
}

func (s *Server) toolMomGet(args map[string]any) (toolCallResult, error) {
	id := stringArg(args, "id")
	if strings.TrimSpace(id) == "" {
		return toolCallResult{}, fmt.Errorf("id is required")
	}
	lib, err := s.requireLibrarian()
	if err != nil {
		return toolCallResult{}, err
	}
	mem, err := lib.Get(id)
	if err != nil {
		return toolCallResult{}, fmt.Errorf("mom_get: %w", err)
	}
	text, _ := json.Marshal(mem)
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(text)}}}, nil
}

func (s *Server) toolMomLandmarks(args map[string]any) (toolCallResult, error) {
	limit := intArg(args, "limit", 20)
	lib, err := s.requireLibrarian()
	if err != nil {
		return toolCallResult{}, err
	}
	items, err := lib.Landmarks(limit)
	if err != nil {
		return toolCallResult{}, fmt.Errorf("mom_landmarks: %w", err)
	}
	if len(items) == 0 {
		return toolCallResult{Content: []toolContent{{Type: "text", Text: "No landmarks found."}}}, nil
	}
	text, _ := json.Marshal(items)
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(text)}}}, nil
}

func (s *Server) toolMomRecall(args map[string]any) (toolCallResult, error) {
	query := stringArg(args, "query")
	if strings.TrimSpace(query) == "" {
		return toolCallResult{}, fmt.Errorf("query is required")
	}
	if s.finder == nil {
		if s.openErr != nil {
			return toolCallResult{}, s.openErr
		}
		return toolCallResult{}, errors.New("finder is not available")
	}
	results, err := s.finder.Recall(finder.Options{Query: query, Limit: 5})
	if err != nil {
		if errors.Is(err, finder.ErrEmptyQuery) {
			return toolCallResult{}, fmt.Errorf("query is required")
		}
		return toolCallResult{}, fmt.Errorf("mom_recall: %w", err)
	}
	if len(results) == 0 {
		return toolCallResult{Content: []toolContent{{Type: "text", Text: "No memories matched."}}}, nil
	}
	text, _ := json.Marshal(compactRecallResults(results))
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(text)}}}, nil
}

type recallIndexItem struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Summary        string  `json:"summary,omitempty"`
	Snippet        string  `json:"snippet,omitempty"`
	SessionID      string  `json:"session_id,omitempty"`
	PromotionState string  `json:"promotion_state"`
	Score          float64 `json:"score"`
	Tier           string  `json:"tier"`
}

func compactRecallResults(results []finder.Result) []recallIndexItem {
	items := make([]recallIndexItem, 0, len(results))
	for _, r := range results {
		items = append(items, recallIndexItem{
			ID:             r.ID,
			Type:           r.Type,
			Summary:        r.Summary,
			Snippet:        contentSnippet(r.Content, 160),
			SessionID:      r.SessionID,
			PromotionState: r.PromotionState,
			Score:          r.Score,
			Tier:           r.Tier,
		})
	}
	return items
}

func contentSnippet(content string, limit int) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return ""
	}
	text, _ := raw["text"].(string)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// --- Argument helpers ---

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intArg(args map[string]any, key string, defaultVal int) int {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	}
	return defaultVal
}
