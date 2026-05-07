package diagnose_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/diagnose"
)

// helper: build a minimal SessionLog.
func makeSession(interactions int, toolCalls map[string]diagnose.ToolGroup) diagnose.SessionLog {
	return diagnose.SessionLog{
		SessionID:    "test-id",
		Interactions: interactions,
		ToolCalls:    toolCalls,
	}
}

func TestComputeReport_ZeroSessions(t *testing.T) {
	r := diagnose.ComputeReport(nil)
	if r == nil {
		t.Fatal("expected non-nil report")
	}
	if r.SessionsAnalyzed != 0 {
		t.Errorf("sessions_analyzed: want 0, got %d", r.SessionsAnalyzed)
	}
	if r.TotalInteractions != 0 {
		t.Errorf("total_interactions: want 0, got %d", r.TotalInteractions)
	}
	if r.MemoryFirstRatio != 0 {
		t.Errorf("memory_first_ratio: want 0, got %f", r.MemoryFirstRatio)
	}
}

func TestComputeReport_SingleSession(t *testing.T) {
	session := makeSession(4, map[string]diagnose.ToolGroup{
		"mom_memory": {
			Total: 3,
			Detail: map[string]int{
				"mom_recall":          2,
				"create_memory_draft": 1,
			},
		},
		"codebase_read": {
			Total: 1,
			Detail: map[string]int{
				"Read": 1,
			},
		},
		"mom_cli": {
			Total: 1,
			Detail: map[string]int{
				"mom_status": 1,
			},
		},
	})

	r := diagnose.ComputeReport([]diagnose.SessionLog{session})

	if r.SessionsAnalyzed != 1 {
		t.Errorf("sessions_analyzed: want 1, got %d", r.SessionsAnalyzed)
	}
	if r.TotalInteractions != 4 {
		t.Errorf("total_interactions: want 4, got %d", r.TotalInteractions)
	}

	// memory-first ratio: 3 / (3+1) = 0.75
	wantMFR := 0.75
	if r.MemoryFirstRatio != wantMFR {
		t.Errorf("memory_first_ratio: want %.2f, got %.2f", wantMFR, r.MemoryFirstRatio)
	}

	// recall efficiency: 2 / 4 = 0.5
	wantRE := 0.5
	if r.RecallEfficiency != wantRE {
		t.Errorf("recall_efficiency: want %.2f, got %.2f", wantRE, r.RecallEfficiency)
	}

	// write-back rate: 1 / 4 = 0.25
	wantWBR := 0.25
	if r.WriteBackRate != wantWBR {
		t.Errorf("write_back_rate: want %.2f, got %.2f", wantWBR, r.WriteBackRate)
	}

	// protocol compliance: 1/1 = 1.0
	if r.ProtocolCompliance != 1.0 {
		t.Errorf("protocol_compliance: want 1.0, got %.2f", r.ProtocolCompliance)
	}
}

func TestComputeReport_MultipleSessions(t *testing.T) {
	s1 := makeSession(2, map[string]diagnose.ToolGroup{
		"mom_memory": {
			Total:  2,
			Detail: map[string]int{"mom_recall": 2},
		},
		"codebase_read": {
			Total:  2,
			Detail: map[string]int{"Read": 2},
		},
	})
	s2 := makeSession(6, map[string]diagnose.ToolGroup{
		"mom_memory": {
			Total:  6,
			Detail: map[string]int{"search_memories": 6},
		},
		"codebase_read": {
			Total:  2,
			Detail: map[string]int{"Grep": 2},
		},
	})

	r := diagnose.ComputeReport([]diagnose.SessionLog{s1, s2})

	if r.SessionsAnalyzed != 2 {
		t.Errorf("sessions_analyzed: want 2, got %d", r.SessionsAnalyzed)
	}
	if r.TotalInteractions != 8 {
		t.Errorf("total_interactions: want 8, got %d", r.TotalInteractions)
	}

	// memory-first ratio: (2+6) / (2+6+2+2) = 8/12 ≈ 0.6667
	wantMFR := 8.0 / 12.0
	if r.MemoryFirstRatio != wantMFR {
		t.Errorf("memory_first_ratio: want %.4f, got %.4f", wantMFR, r.MemoryFirstRatio)
	}

	// recall: (2+6) / 8 interactions = 1.0
	wantRE := 1.0
	if r.RecallEfficiency != wantRE {
		t.Errorf("recall_efficiency: want %.2f, got %.2f", wantRE, r.RecallEfficiency)
	}
}

func TestComputeReport_ProtocolCompliance(t *testing.T) {
	withStatus := makeSession(1, map[string]diagnose.ToolGroup{
		"mom_cli": {Total: 1, Detail: map[string]int{"mom_status": 1}},
	})
	withoutStatus := makeSession(1, map[string]diagnose.ToolGroup{
		"mom_cli": {Total: 1, Detail: map[string]int{"mom_draft": 1}},
	})
	noMomCli := makeSession(1, map[string]diagnose.ToolGroup{})

	r := diagnose.ComputeReport([]diagnose.SessionLog{withStatus, withoutStatus, noMomCli})

	// 1 out of 3 sessions have mom_status
	want := 1.0 / 3.0
	if r.ProtocolCompliance != want {
		t.Errorf("protocol_compliance: want %.4f, got %.4f", want, r.ProtocolCompliance)
	}
}

func TestComputeReport_ContextRediscovery(t *testing.T) {
	session := makeSession(5, map[string]diagnose.ToolGroup{
		"system": {
			Total:  4,
			Detail: map[string]int{"git": 2, "grep": 2},
		},
		"mom_memory": {
			Total:  4,
			Detail: map[string]int{"mom_recall": 4},
		},
	})

	r := diagnose.ComputeReport([]diagnose.SessionLog{session})

	// total tool calls: 4 (system) + 4 (mom_memory) = 8
	// git+grep in system = 4
	// context_rediscovery = 4/8 = 0.5
	want := 0.5
	if r.ContextRediscovery != want {
		t.Errorf("context_rediscovery: want %.2f, got %.2f", want, r.ContextRediscovery)
	}
}

func TestFormatReport(t *testing.T) {
	r := &diagnose.Report{
		SessionsAnalyzed:   3,
		TotalInteractions:  10,
		MemoryFirstRatio:   0.75,
		RecallEfficiency:   0.40,
		ContextRediscovery: 0.10,
		WriteBackRate:      0.20,
		ProtocolCompliance: 1.0,
	}

	out := diagnose.FormatReport(r)

	checks := []string{
		"MOM Diagnose",
		"Sessions analyzed:",
		"Memory-first ratio:",
		"Recall efficiency:",
		"Context rediscovery:",
		"Write-back rate:",
		"Protocol compliance:",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("FormatReport output missing %q", want)
		}
	}
}

func TestFormatReport_Warnings(t *testing.T) {
	// High context rediscovery should trigger warning.
	r := &diagnose.Report{
		SessionsAnalyzed:   1,
		MemoryFirstRatio:   0.3,
		ContextRediscovery: 0.5,
	}

	out := diagnose.FormatReport(r)

	if !strings.Contains(out, "Context rediscovery is high") {
		t.Error("expected context rediscovery warning")
	}
	if !strings.Contains(out, "Memory-first ratio below target") {
		t.Error("expected memory-first ratio warning")
	}
}

func TestLoadSessionLogs(t *testing.T) {
	dir := t.TempDir()

	// Write two valid session files.
	sessions := []diagnose.SessionLog{
		{
			SessionID:    "s1",
			Interactions: 3,
			ToolCalls: map[string]diagnose.ToolGroup{
				"mom_memory": {Total: 1, Detail: map[string]int{"mom_recall": 1}},
			},
		},
		{
			SessionID:    "s2",
			Interactions: 5,
			ToolCalls:    map[string]diagnose.ToolGroup{},
		},
	}
	for _, s := range sessions {
		data, _ := json.Marshal(s)
		if err := os.WriteFile(filepath.Join(dir, "session-"+s.SessionID+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}

	// Write a non-session file that should be ignored.
	os.WriteFile(filepath.Join(dir, "other.json"), []byte(`{}`), 0600)

	loaded, err := diagnose.LoadSessionLogs(dir, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded) != 2 {
		t.Errorf("want 2 sessions, got %d", len(loaded))
	}
}

func TestLoadSessionLogs_LastN(t *testing.T) {
	dir := t.TempDir()

	for i := 1; i <= 5; i++ {
		s := diagnose.SessionLog{SessionID: "s" + string(rune('0'+i))}
		data, _ := json.Marshal(s)
		name := filepath.Join(dir, "session-"+s.SessionID+".json")
		os.WriteFile(name, data, 0600)
	}

	loaded, err := diagnose.LoadSessionLogs(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Errorf("want 3 sessions (lastN=3), got %d", len(loaded))
	}
}

func TestLoadSessionLogs_MissingDir(t *testing.T) {
	_, err := diagnose.LoadSessionLogs("/nonexistent/path", 0)
	if err == nil {
		t.Error("expected error for missing directory")
	}
}
