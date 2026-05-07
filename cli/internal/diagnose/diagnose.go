// Package diagnose reads Logbook session-log JSON files and computes
// derived health metrics for MOM agent sessions.
package diagnose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SessionLog struct {
	SessionID       string               `json:"session_id"`
	Started         string               `json:"started"`
	Ended           string               `json:"ended"`
	Interactions    int                  `json:"interactions"`
	FilesChanged    int                  `json:"files_changed"`
	MemoriesCreated int                  `json:"memories_created"`
	ToolCalls       map[string]ToolGroup `json:"tool_calls"`
}

type ToolGroup struct {
	Total  int            `json:"total"`
	Detail map[string]int `json:"detail"`
}

// Report holds the derived metrics computed from one or more session logs.
type Report struct {
	SessionsAnalyzed   int     `json:"sessions_analyzed"`
	TotalInteractions  int     `json:"total_interactions"`
	MemoryFirstRatio   float64 `json:"memory_first_ratio"`
	RecallEfficiency   float64 `json:"recall_efficiency"`
	ContextRediscovery float64 `json:"context_rediscovery"`
	WriteBackRate      float64 `json:"write_back_rate"`
	ProtocolCompliance float64 `json:"protocol_compliance"`
}

// ComputeReport computes derived metrics from session logs.
func ComputeReport(sessions []SessionLog) *Report {
	if len(sessions) == 0 {
		return &Report{}
	}

	r := &Report{SessionsAnalyzed: len(sessions)}

	totalMemory := 0
	totalCodebaseRead := 0
	totalRecall := 0
	totalInteractions := 0
	totalDraftCreated := 0
	totalToolCalls := 0
	totalGitGrep := 0
	protocolCompliant := 0

	for _, s := range sessions {
		totalInteractions += s.Interactions

		if mem, ok := s.ToolCalls["mom_memory"]; ok {
			totalMemory += mem.Total
			if recall, ok := mem.Detail["mom_recall"]; ok {
				totalRecall += recall
			}
			if recall, ok := mem.Detail["search_memories"]; ok {
				totalRecall += recall
			}
			if draft, ok := mem.Detail["create_memory_draft"]; ok {
				totalDraftCreated += draft
			}
		}

		if read, ok := s.ToolCalls["codebase_read"]; ok {
			totalCodebaseRead += read.Total
		}

		// Count all tool calls.
		for _, group := range s.ToolCalls {
			totalToolCalls += group.Total
		}

		// Context rediscovery: git + grep in system category.
		if sys, ok := s.ToolCalls["system"]; ok {
			for tool, count := range sys.Detail {
				if strings.Contains(tool, "git") || strings.Contains(tool, "grep") {
					totalGitGrep += count
				}
			}
		}
		if read, ok := s.ToolCalls["codebase_read"]; ok {
			for tool, count := range read.Detail {
				if tool == "grep" || tool == "Grep" || tool == "rg" {
					totalGitGrep += count
				}
			}
		}

		// Protocol compliance: did mom_status get called?
		if cli, ok := s.ToolCalls["mom_cli"]; ok {
			if _, ok := cli.Detail["mom_status"]; ok {
				protocolCompliant++
			}
		}
	}

	r.TotalInteractions = totalInteractions

	// Memory-first ratio: mom_memory / (mom_memory + codebase_read).
	if totalMemory+totalCodebaseRead > 0 {
		r.MemoryFirstRatio = float64(totalMemory) / float64(totalMemory+totalCodebaseRead)
	}

	// Recall efficiency: recall calls / interactions.
	if totalInteractions > 0 {
		r.RecallEfficiency = float64(totalRecall) / float64(totalInteractions)
	}

	// Context rediscovery: (git + grep) / total tool calls.
	if totalToolCalls > 0 {
		r.ContextRediscovery = float64(totalGitGrep) / float64(totalToolCalls)
	}

	// Write-back rate: create_memory_draft / interactions.
	if totalInteractions > 0 {
		r.WriteBackRate = float64(totalDraftCreated) / float64(totalInteractions)
	}

	// Protocol compliance: % of sessions with mom_status call.
	r.ProtocolCompliance = float64(protocolCompliant) / float64(len(sessions))

	return r
}

// FormatReport returns a human-readable string representation.
func FormatReport(r *Report) string {
	var b strings.Builder
	b.WriteString("\nMOM Diagnose — session health\n")
	b.WriteString(strings.Repeat("─", 40) + "\n")
	fmt.Fprintf(&b, "Sessions analyzed:     %d\n", r.SessionsAnalyzed)
	fmt.Fprintf(&b, "Total interactions:   %d\n\n", r.TotalInteractions)
	fmt.Fprintf(&b, "Memory-first ratio:   %.2f  (target: > 0.5)\n", r.MemoryFirstRatio)
	fmt.Fprintf(&b, "Recall efficiency:    %.2f  (target: > 0.3)\n", r.RecallEfficiency)
	fmt.Fprintf(&b, "Context rediscovery:  %.2f  (target: < 0.2)\n", r.ContextRediscovery)
	fmt.Fprintf(&b, "Write-back rate:      %.2f  (target: > 0.1)\n", r.WriteBackRate)
	compliance := int(r.ProtocolCompliance * float64(r.SessionsAnalyzed))
	compliancePct := int(r.ProtocolCompliance * 100)
	fmt.Fprintf(&b, "Protocol compliance:  %d/%d sessions (%d%%)\n", compliance, r.SessionsAnalyzed, compliancePct)

	// Warnings.
	if r.ContextRediscovery > 0.2 {
		b.WriteString("\n! Context rediscovery is high — memory coverage gap detected.\n")
	}
	if r.MemoryFirstRatio < 0.5 && r.SessionsAnalyzed > 0 {
		b.WriteString("! Memory-first ratio below target — agent may be bypassing memory.\n")
	}

	return b.String()
}

// LoadSessionLogs reads all session-*.json files from logsDir.
// If lastN > 0, only the last N files (by filesystem sort order) are returned.
func LoadSessionLogs(logsDir string, lastN int) ([]SessionLog, error) {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return nil, err
	}

	var sessions []SessionLog
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "session-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(logsDir, e.Name()))
		if err != nil {
			continue
		}
		var s SessionLog
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	if lastN > 0 && len(sessions) > lastN {
		sessions = sessions[len(sessions)-lastN:]
	}

	return sessions, nil
}
