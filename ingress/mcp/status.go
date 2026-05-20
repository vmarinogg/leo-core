package mcp

import (
	"encoding/json"
	"os"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/ops/daemon"
	"github.com/momhq/mom/storage/librarian"
)

// statusPayload defines the compact mom_status response.
type statusPayload struct {
	Identity   statusIdentityBlock   `json:"identity"`
	Health     statusHealthBlock     `json:"health"`
	Routing    statusRoutingBlock    `json:"routing"`
	Session    statusSessionBlock    `json:"session"`
	VaultState statusVaultStateBlock `json:"vault_state"`
	Skills     []statusSkillBlock    `json:"skills"`
}

type statusIdentityBlock struct {
	Name    string `json:"name"`
	Tagline string `json:"tagline"`
}

type statusHealthBlock struct {
	Status string `json:"status"`
}

type statusRoutingBlock struct {
	Startup   string `json:"startup"`
	Preferred string `json:"preferred"`
	MCP       string `json:"mcp"`
}

type statusSessionBlock struct {
	CWD string `json:"cwd"`
}

type statusVaultStateBlock struct {
	Path          string `json:"path"`
	TotalMemories int    `json:"total_memories"`
	Landmarks     int    `json:"landmarks"`
	RecordMode    string `json:"record_mode"`
	Watcher       string `json:"watcher"`
}

type statusSkillBlock struct {
	Command string `json:"command"`
	Purpose string `json:"purpose"`
}

// toolMomStatus returns MOM's startup handshake as a compact JSON payload.
func (s *Server) toolMomStatus() (toolCallResult, error) {
	payload := statusPayload{
		Identity: statusIdentity(),
		Health: statusHealthBlock{
			Status: "ok",
		},
		Routing: statusRoutingBlock{
			Startup:   "Call mom_status at session start. After this, prefer MOM skills and CLI.",
			Preferred: "Use /mom-status, /mom-recall, /mom-project, /mom-wrap-up, and mom CLI commands first.",
			MCP:       "Fallback for discovery, startup handshake, or when CLI skills are unavailable.",
		},
		Session:    statusSession(),
		VaultState: s.statusVaultState(),
		Skills:     statusSkills(),
	}
	if payload.VaultState.Path == "" {
		payload.Health.Status = "degraded"
	}

	text, err := json.Marshal(payload)
	if err != nil {
		return toolCallResult{}, err
	}
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(text)}}}, nil
}

func statusIdentity() statusIdentityBlock {
	return statusIdentityBlock{
		Name:    "MOM",
		Tagline: "Memory Oriented Machine — she remembers, so you don't have to_",
	}
}

func statusSession() statusSessionBlock {
	cwd, _ := os.Getwd()
	return statusSessionBlock{CWD: cwd}
}

func statusSkills() []statusSkillBlock {
	return []statusSkillBlock{
		{Command: "/mom-status", Purpose: "Show MOM health and vault state via mom status."},
		{Command: "/mom-recall", Purpose: "Search persistent memory with mom recall."},
		{Command: "/mom-project", Purpose: "Bind the current directory to a MOM project id with mom project bind."},
		{Command: "/mom-wrap-up", Purpose: "Curate recent drafts with mom drafts and mom curate."},
	}
}

func (s *Server) statusVaultState() statusVaultStateBlock {
	path, _ := canonical.Path()
	state := statusVaultStateBlock{
		Path:       path,
		RecordMode: "continuous",
		Watcher:    watcherState(),
	}

	lib, err := s.requireLibrarian()
	if err != nil {
		return state
	}

	memories, _ := lib.SearchMemories(librarian.SearchFilter{Limit: 1_000_000})
	landmarks, _ := lib.Landmarks(1_000_000)
	state.TotalMemories = len(memories)
	state.Landmarks = len(landmarks)
	return state
}

func watcherState() string {
	health, err := daemon.StatusGlobal()
	if err != nil || len(health.Services) == 0 {
		return "unknown"
	}
	if health.Services[0].DaemonRunning {
		return "active"
	}
	return "inactive"
}
