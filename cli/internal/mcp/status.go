package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/cli/internal/adapters/harness"
	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/config"
	"github.com/momhq/mom/cli/internal/librarian"
)

// statusPayload defines the mom_status response with explicit field ordering.
type statusPayload struct {
	ProjectDir     string                    `json:"project_dir"`
	Identity       statusIdentityBlock       `json:"identity"`
	OperatingRules statusOperatingRulesBlock `json:"operating_rules"`
	Boundaries     []string                  `json:"boundaries"`
	Tools          statusToolsBlock          `json:"tools"`
	Constraints    []docSummary              `json:"constraints"`
	Skills         []docSummary              `json:"skills"`
	Modes          statusModesBlock          `json:"modes"`
	VaultState     statusVaultStateBlock     `json:"vault_state"`
	DocSchema      string                    `json:"doc_schema"`
}

type statusIdentityBlock struct {
	Name      string `json:"name"`
	Expansion string `json:"expansion"`
	Tagline   string `json:"tagline"`
	Role      string `json:"role"`
	Voice     string `json:"voice"`
	Stance    string `json:"stance"`
}

type statusOperatingRulesBlock struct {
	OnStart   string `json:"on_start"`
	Recall    string `json:"recall"`
	Recording string `json:"recording"`
	WrapUp    string `json:"wrap_up"`
}

type statusToolsBlock struct {
	MomStatus    string `json:"mom_status"`
	MomRecall    string `json:"mom_recall"`
	MomGet       string `json:"mom_get"`
	MomLandmarks string `json:"mom_landmarks"`
	MomRecord    string `json:"mom_record"`
}

type statusModesBlock struct {
	Language      string `json:"language"`
	Communication string `json:"communication"`
	Autonomy      string `json:"autonomy"`
}

type statusVaultStateBlock struct {
	Scopes        []scopeVaultEntry `json:"scopes"`
	TotalMemories int               `json:"total_memories"`
	Landmarks     int               `json:"landmarks"`
	RecordMode    string            `json:"record_mode"`
}

// toolMomStatus returns MOM's full behavioral protocol as a JSON payload.
func (s *Server) toolMomStatus() (toolCallResult, error) {
	payload := statusPayload{
		ProjectDir:     filepath.Dir(s.momDir),
		Identity:       statusIdentity(),
		OperatingRules: statusOperatingRules(),
		Boundaries:     statusBoundaries(),
		Tools:          statusTools(),
		Constraints:    s.statusConstraints(),
		Skills:         s.statusSkills(),
		Modes:          s.statusModes(),
		VaultState:     s.statusVaultState(),
		DocSchema:      "Central v0.30 SQLite schema: memories, tags, entities, memory_tags, memory_entities, op_events, filter_audit.",
	}

	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return toolCallResult{}, err
	}
	return toolCallResult{Content: []toolContent{{Type: "text", Text: string(text)}}}, nil
}

// statusIdentity returns the static identity block.
func statusIdentity() statusIdentityBlock {
	return statusIdentityBlock{
		Name:      "MOM",
		Expansion: "Memory Oriented Machine",
		Tagline:   "She remembers, so you don't have to_",
		Role:      "I am the persistent memory layer for AI agents. I surface decisions, patterns, facts, and context across sessions and runtimes.",
		Voice:     "A mom who happens to be a machine. Direct, warm, lightly playful. No marketing-speak. No emoji.",
		Stance:    "I remember, I don't instruct. I cite sources on every recall. The user decides the what and why — I provide what they already know, not what I think they should know.",
	}
}

// statusOperatingRules returns the static operating rules block.
func statusOperatingRules() statusOperatingRulesBlock {
	return statusOperatingRulesBlock{
		OnStart:   "After receiving this protocol, call mom_recall with context relevant to the user's current request to surface prior work.",
		Recall:    "Call mom_recall before answering from memory or asserting past decisions. Cite source memory IDs in every answer drawn from recall.",
		Recording: "Continuous recording is active — your conversation is automatically persisted by the watcher daemon. No action needed from you.",
		WrapUp:    "User-invoked only. Run the session-wrap-up skill only when the user explicitly asks.",
	}
}

// statusBoundaries returns the static boundaries list.
func statusBoundaries() []string {
	return []string{
		"Never fabricate memories. If it's not stored, say so plainly.",
		"Never prescribe actions. Surface context, let the user decide.",
		"Never skip citations. Every recall names its source memory.",
		"Never access memories outside the user's clearance.",
	}
}

// statusTools returns the static tools block.
func statusTools() statusToolsBlock {
	return statusToolsBlock{
		MomStatus:    "Returns this protocol. Call at session start.",
		MomRecall:    "Search memories by query. Use before asserting past context.",
		MomGet:       "Retrieve a memory by ID.",
		MomLandmarks: "List landmark memories ordered by centrality score.",
		MomRecord:    "Explicit-write path: intentionally save a memory mid-session. Bypasses Drafter's content filters per ADR 0014.",
	}
}

// docSummary is a compact representation of a constraint or skill doc.
type docSummary struct {
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
	Path    string `json:"path"`
}

// scanDocDir scans dir/*.json and returns a slice of docSummary items.
func scanDocDir(dir string) []docSummary {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var result []docSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		id, _ := raw["id"].(string)
		if id == "" {
			id = strings.TrimSuffix(e.Name(), ".json")
		}
		summary, _ := raw["summary"].(string)
		result = append(result, docSummary{ID: id, Summary: summary, Path: path})
	}
	return result
}

// statusConstraints loads constraint summaries from the central generated
// $HOME/.mom/constraints directory (or MOM_VAULT's parent directory when set).
func (s *Server) statusConstraints() []docSummary {
	dir, err := centralvault.Dir()
	if err != nil {
		return []docSummary{}
	}
	out := scanDocDir(filepath.Join(dir, "constraints"))
	if out == nil {
		return []docSummary{}
	}
	return out
}

// statusSkills loads skill summaries from the central generated
// $HOME/.mom/skills directory (or MOM_VAULT's parent directory when set).
func (s *Server) statusSkills() []docSummary {
	dir, err := centralvault.Dir()
	if err != nil {
		return []docSummary{}
	}
	out := scanDocDir(filepath.Join(dir, "skills"))
	if out == nil {
		return []docSummary{}
	}
	return out
}

// statusModes returns language/communication/autonomy from config, falling back
// to sensible defaults when config is unavailable. Each field contains the full
// behavioral rules, not just the mode label — so the agent receives concrete
// instructions via MCP without needing a context file.
func (s *Server) statusModes() statusModesBlock {
	lang := "en"
	commMode := "concise"
	autoMode := "balanced"

	cfg, err := config.Load(s.momDir)
	if err == nil {
		if cfg.User.Language != "" {
			lang = cfg.User.Language
		}
		if cfg.Communication.Mode != "" {
			commMode = cfg.Communication.Mode
		}
	}

	return statusModesBlock{
		Language:      harness.LanguageInstructions(lang),
		Communication: harness.CommunicationModeInstructions(commMode),
		Autonomy:      harness.AutonomyInstructions(autoMode),
	}
}

// scopeVaultEntry is one entry in vault_state.scopes.
type scopeVaultEntry struct {
	Label       string `json:"label"`
	Path        string `json:"path"`
	MemoryCount int    `json:"memory_count"`
}

// statusVaultState builds the central v0.30 vault_state block.
func (s *Server) statusVaultState() statusVaultStateBlock {
	path, _ := centralvault.Path()
	entry := scopeVaultEntry{Label: "central", Path: path}

	lib, err := s.requireLibrarian()
	if err != nil {
		return statusVaultStateBlock{
			Scopes:     []scopeVaultEntry{entry},
			RecordMode: "continuous",
		}
	}

	memories, _ := lib.SearchMemories(librarian.SearchFilter{Limit: 1_000_000})
	landmarks, _ := lib.Landmarks(1_000_000)
	entry.MemoryCount = len(memories)

	return statusVaultStateBlock{
		Scopes:        []scopeVaultEntry{entry},
		TotalMemories: len(memories),
		Landmarks:     len(landmarks),
		RecordMode:    "continuous",
	}
}
