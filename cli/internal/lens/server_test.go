package lens

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/archtest"
	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/vault"
)

func testLibrarian(t *testing.T) (*librarian.Librarian, func()) {
	t.Helper()
	v, err := vault.Open(t.TempDir()+"/mom.db", centralvault.Migrations())
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	return librarian.New(v), func() { _ = v.Close() }
}

func newTestServer(t *testing.T, lib *librarian.Librarian) *Server {
	t.Helper()
	srv, err := New(lib)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func insertTurn(t *testing.T, lib *librarian.Librarian, sessionID, role string, at time.Time, payload map[string]any) {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	payload["role"] = role
	_, err := lib.InsertOpEvent(librarian.OpEvent{EventType: "turn.observed", SessionID: sessionID, CreatedAt: at, Payload: payload})
	if err != nil {
		t.Fatalf("InsertOpEvent: %v", err)
	}
}

func insertLegacySummary(t *testing.T, lib *librarian.Librarian, sessionID string, at time.Time, payload map[string]any) {
	t.Helper()
	_, err := lib.InsertOpEvent(librarian.OpEvent{EventType: "legacy.session.summary", SessionID: sessionID, CreatedAt: at, Payload: payload})
	if err != nil {
		t.Fatalf("InsertOpEvent: %v", err)
	}
}

func insertMemory(t *testing.T, lib *librarian.Librarian, sessionID, summary string, tags []string, curated bool) string {
	t.Helper()
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "semantic",
		Summary:                summary,
		Content:                `{"text":"` + summary + `"}`,
		CreatedAt:              time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
		SessionID:              sessionID,
		ProvenanceActor:        "test",
		ProvenanceSourceType:   "test",
		ProvenanceTriggerEvent: "test",
	}, tags)
	if err != nil {
		t.Fatalf("InsertMemoryWithTags: %v", err)
	}
	if curated {
		state := "curated"
		if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
			t.Fatalf("UpdateOperational: %v", err)
		}
	}
	return id
}

func fetchSessions(t *testing.T, h http.Handler) []SessionSummary {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []SessionSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

func fetchDetail(t *testing.T, h http.Handler, id string) SessionDetail {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got SessionDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

func TestSessions_RenderFromCentralTurnObserved(t *testing.T) {
	lib, closeFn := testLibrarian(t)
	defer closeFn()
	at := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	insertTurn(t, lib, "s-new", "user", at, nil)
	insertTurn(t, lib, "s-new", "assistant", at.Add(time.Minute), map[string]any{
		"tool_categories": []any{"codebase_read", "mom_cli"},
		"tool_names":      []any{"Read", "mom recall"},
		"usage":           map[string]any{"total_tokens": float64(42), "cost_usd": 0.02},
		"model":           "claude-sonnet",
		"provider":        "anthropic",
	})

	got := fetchSessions(t, newTestServer(t, lib).Handler())
	if len(got) != 1 {
		t.Fatalf("sessions = %+v", got)
	}
	if got[0].SessionID != "s-new" || got[0].Interactions != 1 || got[0].ToolsTotal != 2 || got[0].TotalTokens != 42 || got[0].ScopeLabel != "central" {
		t.Fatalf("summary = %+v", got[0])
	}
	detail := fetchDetail(t, newTestServer(t, lib).Handler(), "s-new")
	if detail.ToolCalls["mom_cli"].Detail["mom recall"] != 1 || detail.ToolCalls["codebase_read"].Detail["Read"] != 1 {
		t.Fatalf("tool detail missing safe tool names: %+v", detail.ToolCalls)
	}
}

func TestSessions_RenderLegacySummaryFromCentralEvents(t *testing.T) {
	lib, closeFn := testLibrarian(t)
	defer closeFn()
	insertLegacySummary(t, lib, "s-legacy", time.Date(2026, 4, 10, 15, 0, 0, 0, time.UTC), map[string]any{
		"legacy_format": "session_summary",
		"started":       "2026-04-10T14:00:00Z",
		"ended":         "2026-04-10T15:00:00Z",
		"interactions":  float64(7),
		"tool_categories": map[string]any{
			"codebase_read": float64(4),
			"system":        float64(3),
		},
		"usage":    map[string]any{"total_tokens": float64(100), "cost_usd": 0.5},
		"model":    "legacy-model",
		"provider": "legacy-provider",
	})

	got := fetchSessions(t, newTestServer(t, lib).Handler())
	if len(got) != 1 || got[0].Interactions != 7 || got[0].ToolsTotal != 7 || got[0].TotalTokens != 100 || got[0].DurationSecs != 3600 {
		t.Fatalf("sessions = %+v", got)
	}
}

func TestSessions_MemoryOnlySessionAppearsWithTags(t *testing.T) {
	lib, closeFn := testLibrarian(t)
	defer closeFn()
	insertMemory(t, lib, "s-memory", "remember this", []string{"deploy", "aws"}, true)

	srv := newTestServer(t, lib)
	got := fetchSessions(t, srv.Handler())
	if len(got) != 1 || got[0].SessionID != "s-memory" || got[0].MemoryCount != 1 || got[0].CuratedCount != 1 || got[0].Interactions != 0 {
		t.Fatalf("sessions = %+v", got)
	}
	detail := fetchDetail(t, srv.Handler(), "s-memory")
	if len(detail.Memories) != 1 || len(detail.Memories[0].Tags) != 2 || detail.Memories[0].PromotionState != "curated" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestSessions_ExcludePseudoLegacySessionWithoutMemories(t *testing.T) {
	lib, closeFn := testLibrarian(t)
	defer closeFn()
	_, err := lib.InsertOpEvent(librarian.OpEvent{EventType: "legacy.consumption", SessionID: "legacy:abc:2026-04-10", CreatedAt: time.Now(), Payload: map[string]any{"kind": "ConsumptionEvent"}})
	if err != nil {
		t.Fatal(err)
	}
	got := fetchSessions(t, newTestServer(t, lib).Handler())
	if len(got) != 0 {
		t.Fatalf("sessions = %+v, want none", got)
	}
}

func TestSessions_MixedSessionPrefersTurnObservedCounts(t *testing.T) {
	lib, closeFn := testLibrarian(t)
	defer closeFn()
	insertLegacySummary(t, lib, "s-mixed", time.Date(2026, 4, 10, 15, 0, 0, 0, time.UTC), map[string]any{
		"started":         "2026-04-10T14:00:00Z",
		"ended":           "2026-04-10T15:00:00Z",
		"interactions":    float64(99),
		"tool_categories": map[string]any{"system": float64(99)},
		"model":           "legacy-model",
		"provider":        "legacy-provider",
		"legacy_format":   "session_summary",
	})
	insertTurn(t, lib, "s-mixed", "assistant", time.Date(2026, 4, 10, 14, 30, 0, 0, time.UTC), map[string]any{
		"tool_categories": []any{"codebase_read"},
		"model":           "new-model",
		"provider":        "new-provider",
	})

	got := fetchSessions(t, newTestServer(t, lib).Handler())
	if len(got) != 1 || got[0].Interactions != 1 || got[0].ToolsTotal != 1 || got[0].Model != "new-model" || got[0].Provider != "new-provider" || got[0].DurationSecs != 3600 {
		t.Fatalf("sessions = %+v", got)
	}
}

func TestLensResponseDoesNotLeakToolInputSentinel(t *testing.T) {
	lib, closeFn := testLibrarian(t)
	defer closeFn()
	insertTurn(t, lib, "s-private", "assistant", time.Now(), map[string]any{
		"tool_categories": []any{"system"},
		"input":           "AKIA-SHOULD-NOT-LEAK secrets.env echo boom",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s-private", nil)
	rec := httptest.NewRecorder()
	newTestServer(t, lib).Handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "AKIA") || strings.Contains(rec.Body.String(), "secrets.env") || strings.Contains(rec.Body.String(), "echo boom") {
		t.Fatalf("Lens response leaked sentinel: %s", rec.Body.String())
	}
}

func TestLensStaticHasNoScopeFilter(t *testing.T) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(data)
	for _, forbidden := range []string{"v3-scope-row", "activeScope", "?scope=", "Scope</span>", "nav-scope"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("static UI still contains scope filter marker %q", forbidden)
		}
	}
}

func TestLensDoesNotImportLegacyStores(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/cli/internal/memory",
		"github.com/momhq/mom/cli/internal/scope",
		"github.com/momhq/mom/cli/internal/logbook",
	)
}

func TestListenWithFallback_BumpsToNextPort(t *testing.T) {
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("seed listen: %v", err)
	}
	defer occupier.Close()

	taken := occupier.Addr().(*net.TCPAddr).Port

	ln, err := ListenWithFallback("127.0.0.1", taken, 5)
	if err != nil {
		t.Fatalf("ListenWithFallback: %v", err)
	}
	defer ln.Close()

	got := ln.Addr().(*net.TCPAddr).Port
	if got == taken {
		t.Fatalf("expected fallback port, got the occupied one (%d)", got)
	}
	if got < taken+1 || got > taken+5 {
		t.Fatalf("expected port in [%d, %d], got %d", taken+1, taken+5, got)
	}
}

func TestListenWithFallback_ZeroAttemptsFailsCleanly(t *testing.T) {
	occupier, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("seed listen: %v", err)
	}
	defer occupier.Close()

	taken := occupier.Addr().(*net.TCPAddr).Port

	if _, err := ListenWithFallback("127.0.0.1", taken, 0); err == nil {
		t.Fatalf("expected error when port is taken and attempts=0")
	}
}
