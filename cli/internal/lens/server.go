// Package lens serves the mom sessions dashboard as a local HTTP server.
package lens

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/librarian"
)

//go:embed static
var staticFS embed.FS

const centralScopeLabel = "central"

type ToolGroup struct {
	Total  int            `json:"total"`
	Detail map[string]int `json:"detail"`
}

// SessionSummary is the list-view shape returned by GET /api/sessions.
type SessionSummary struct {
	SessionID    string  `json:"session_id"`
	ScopeLabel   string  `json:"scope_label"`
	Started      string  `json:"started"`
	Ended        string  `json:"ended"`
	DurationSecs float64 `json:"duration_secs"`
	Interactions int     `json:"interactions"`
	MemoryCount  int     `json:"memory_count"`
	CuratedCount int     `json:"curated_count"`
	ToolsTotal   int     `json:"tools_total"`
	TotalTokens  int     `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
}

// MemoryItem is a memory document linked to a session.
type MemoryItem struct {
	ID             string    `json:"id"`
	Summary        string    `json:"summary"`
	Tags           []string  `json:"tags"`
	PromotionState string    `json:"promotion_state"`
	Created        time.Time `json:"created"`
	Landmark       bool      `json:"landmark"`
}

// SessionDetail extends SessionSummary with the full per-session breakdown.
type SessionDetail struct {
	SessionSummary
	ToolCalls map[string]ToolGroup `json:"tool_calls"`
	Memories  []MemoryItem         `json:"memories"`
}

// MetaResponse is returned by GET /api/meta.
type MetaResponse struct {
	TotalSessions int `json:"total_sessions"`
	TotalMemories int `json:"total_memories"`
}

// Server serves the lens dashboard.
type Server struct {
	lib *librarian.Librarian
	mux *http.ServeMux
}

// New creates a Server over the central vault.
func New(lib *librarian.Librarian) (*Server, error) {
	if lib == nil {
		return nil, fmt.Errorf("librarian is nil")
	}
	s := &Server{lib: lib, mux: http.NewServeMux()}

	s.mux.Handle("GET /api/meta", http.HandlerFunc(s.handleMeta))
	s.mux.Handle("GET /api/sessions/{id}", http.HandlerFunc(s.handleSession))
	s.mux.Handle("GET /api/sessions", http.HandlerFunc(s.handleSessions))

	if devDir := os.Getenv("MOM_LENS_STATIC_DIR"); devDir != "" {
		s.mux.Handle("/", http.FileServer(http.Dir(devDir)))
	} else {
		sub, err := fs.Sub(staticFS, "static")
		if err != nil {
			return nil, fmt.Errorf("sub FS: %w", err)
		}
		s.mux.Handle("/", http.FileServer(http.FS(sub)))
	}

	return s, nil
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

type sessionData struct {
	SessionSummary
	ToolCalls map[string]ToolGroup
	Memories  []MemoryItem
}

type sessionBuild struct {
	id              string
	started         time.Time
	ended           time.Time
	interactions    int
	tools           map[string]int
	toolDetails     map[string]map[string]int
	totalTokens     int
	costUSD         float64
	provider        string
	model           string
	hasTurnObserved bool
}

func (s *Server) loadSessionData() (map[string]sessionData, int, error) {
	memIndex, totalMemories, err := s.loadMemoryIndex()
	if err != nil {
		return nil, 0, err
	}
	events, err := s.lib.QueryOpEvents(librarian.OpEventFilter{Limit: 100000})
	if err != nil {
		return nil, 0, err
	}

	builds := map[string]*sessionBuild{}
	get := func(id string) *sessionBuild {
		b := builds[id]
		if b == nil {
			b = &sessionBuild{id: id, tools: map[string]int{}, toolDetails: map[string]map[string]int{}}
			builds[id] = b
		}
		return b
	}
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		b := get(e.SessionID)
		b.includeTime(e.CreatedAt)
		switch e.EventType {
		case "turn.observed":
			applyTurnObserved(b, e.Payload)
		case "legacy.session.summary":
			applyLegacySummary(b, e.Payload)
		}
	}
	for sid, memories := range memIndex {
		if sid == "" {
			continue
		}
		b := get(sid)
		for _, m := range memories {
			b.includeTime(m.Created)
		}
	}

	out := map[string]sessionData{}
	for sid, b := range builds {
		memories := memIndex[sid]
		if strings.HasPrefix(sid, "legacy:") && len(memories) == 0 {
			continue
		}
		if memories == nil {
			memories = []MemoryItem{}
		}
		toolCalls, toolsTotal := toolGroups(b.tools, b.toolDetails)
		curated := 0
		for _, m := range memories {
			if m.PromotionState == "curated" || m.Landmark {
				curated++
			}
		}
		out[sid] = sessionData{
			SessionSummary: SessionSummary{
				SessionID:    sid,
				ScopeLabel:   centralScopeLabel,
				Started:      formatLensTime(b.started),
				Ended:        formatLensTime(b.ended),
				DurationSecs: durationSecs(b.started, b.ended),
				Interactions: b.interactions,
				MemoryCount:  len(memories),
				CuratedCount: curated,
				ToolsTotal:   toolsTotal,
				TotalTokens:  b.totalTokens,
				CostUSD:      b.costUSD,
				Provider:     b.provider,
				Model:        b.model,
			},
			ToolCalls: toolCalls,
			Memories:  memories,
		}
	}
	return out, totalMemories, nil
}

func (s *Server) loadMemoryIndex() (map[string][]MemoryItem, int, error) {
	rows, err := s.lib.SearchMemories(librarian.SearchFilter{Limit: 100000})
	if err != nil {
		return nil, 0, err
	}
	index := map[string][]MemoryItem{}
	for _, row := range rows {
		m := row.Memory
		tags, err := s.lib.TagsForMemory(m.ID)
		if err != nil {
			return nil, 0, err
		}
		if m.SessionID != "" {
			index[m.SessionID] = append(index[m.SessionID], MemoryItem{
				ID:             m.ID,
				Summary:        m.Summary,
				Tags:           tags,
				PromotionState: m.PromotionState,
				Created:        m.CreatedAt,
				Landmark:       m.Landmark,
			})
		}
	}
	return index, len(rows), nil
}

func (b *sessionBuild) includeTime(t time.Time) {
	if t.IsZero() {
		return
	}
	if b.started.IsZero() || t.Before(b.started) {
		b.started = t
	}
	if b.ended.IsZero() || t.After(b.ended) {
		b.ended = t
	}
}

func applyTurnObserved(b *sessionBuild, payload map[string]any) {
	if !b.hasTurnObserved {
		b.interactions = 0
		b.tools = map[string]int{}
		b.toolDetails = map[string]map[string]int{}
		b.totalTokens = 0
		b.costUSD = 0
	}
	b.hasTurnObserved = true
	if s, _ := payload["role"].(string); s == "assistant" {
		b.interactions++
	}
	if model, _ := payload["model"].(string); model != "" {
		b.model = model
	}
	if provider, _ := payload["provider"].(string); provider != "" {
		b.provider = provider
	}
	cats := stringSlice(payload["tool_categories"])
	names := stringSlice(payload["tool_names"])
	for i, cat := range cats {
		if cat == "" {
			continue
		}
		b.tools[cat]++
		if i < len(names) && names[i] != "" {
			if b.toolDetails[cat] == nil {
				b.toolDetails[cat] = map[string]int{}
			}
			b.toolDetails[cat][names[i]]++
		}
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		b.totalTokens += intValue(usage["total_tokens"])
		b.costUSD += floatValue(usage["cost_usd"])
	}
}

func applyLegacySummary(b *sessionBuild, payload map[string]any) {
	b.includePayloadTime(payload, "started")
	b.includePayloadTime(payload, "ended")
	if b.model == "" {
		b.model, _ = payload["model"].(string)
	}
	if b.provider == "" {
		b.provider, _ = payload["provider"].(string)
	}
	if b.hasTurnObserved {
		return
	}
	b.interactions = intValue(payload["interactions"])
	if cats, ok := payload["tool_categories"].(map[string]any); ok {
		for cat, total := range cats {
			b.tools[cat] += intValue(total)
		}
	}
	if usage, ok := payload["usage"].(map[string]any); ok {
		b.totalTokens = intValue(usage["total_tokens"])
		b.costUSD = floatValue(usage["cost_usd"])
	}
}

func (b *sessionBuild) includePayloadTime(payload map[string]any, key string) {
	s, _ := payload[key].(string)
	if s == "" {
		return
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		b.includeTime(t)
	}
}

func stringSlice(v any) []string {
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := strconv.Atoi(n.String())
		return i
	default:
		return 0
	}
}

func floatValue(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := strconv.ParseFloat(n.String(), 64)
		return f
	default:
		return 0
	}
}

func toolGroups(tools map[string]int, details map[string]map[string]int) (map[string]ToolGroup, int) {
	out := map[string]ToolGroup{}
	total := 0
	for cat, n := range tools {
		if n <= 0 {
			continue
		}
		detail := details[cat]
		if detail == nil {
			detail = map[string]int{}
		}
		out[cat] = ToolGroup{Total: n, Detail: detail}
		total += n
	}
	return out, total
}

func durationSecs(started, ended time.Time) float64 {
	if started.IsZero() || ended.IsZero() || !ended.After(started) {
		return 0
	}
	return math.Round(ended.Sub(started).Seconds())
}

func formatLensTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sortedSessions(m map[string]sessionData) []SessionSummary {
	out := make([]SessionSummary, 0, len(m))
	for _, s := range m {
		out = append(out, s.SessionSummary)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Started > out[j].Started
	})
	return out
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	sessions, totalMemories, err := s.loadSessionData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, MetaResponse{TotalSessions: len(sessions), TotalMemories: totalMemories})
}

func (s *Server) handleSessions(w http.ResponseWriter, _ *http.Request) {
	sessions, _, err := s.loadSessionData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, sortedSessions(sessions))
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	sessions, _, err := s.loadSessionData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d, ok := sessions[id]
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, SessionDetail{SessionSummary: d.SessionSummary, ToolCalls: d.ToolCalls, Memories: d.Memories})
}

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
