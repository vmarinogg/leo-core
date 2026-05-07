package harness

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed capabilities/claude.yaml
var claudeCapabilitiesYAML []byte

// ClaudeAdapter implements the Adapter interface for Claude Code.
// It reads from .mom/ and generates .claude/CLAUDE.md + settings.json.
type ClaudeAdapter struct {
	projectRoot string
}

// NewClaudeAdapter creates a ClaudeAdapter for the given project root.
func NewClaudeAdapter(projectRoot string) *ClaudeAdapter {
	return &ClaudeAdapter{projectRoot: projectRoot}
}

func (a *ClaudeAdapter) Name() string {
	return "claude"
}

func (a *ClaudeAdapter) Tier() Tier {
	return Fluent
}

func (a *ClaudeAdapter) GenerateContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	claudeDir := filepath.Join(a.projectRoot, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}

	var body string
	if config.Delivery == "context-file" {
		body = BuildContextContent(config, constraints, skills, identity)
	} else {
		body = BuildMinimalContextContent()
	}
	content := a.Watermark() + "\n\n" + body

	contextFile := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(contextFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing CLAUDE.md: %w", err)
	}

	return nil
}

// DefaultTranscriptDir returns Claude Code's transcript directory.
func (a *ClaudeAdapter) DefaultTranscriptDir() string {
	return "~/.claude/projects/"
}

func (a *ClaudeAdapter) RegisterHooks() error {
	claudeDir := filepath.Join(a.projectRoot, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")

	// Ensure .claude/ exists.
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}

	// Load existing settings or start fresh.
	settings := make(map[string]any)
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parsing settings.json: %w", err)
		}
	}

	settings["hooks"] = claudeHookSettings()

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	data = append(data, '\n')
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		return fmt.Errorf("writing settings.json: %w", err)
	}

	return nil
}

func (a *ClaudeAdapter) DetectHarness() bool {
	if commandExists("claude") {
		return true
	}
	if path, err := homePath(".claude"); err == nil && pathExists(path) {
		return true
	}
	if path, err := homePath(".claude.json"); err == nil && pathExists(path) {
		return true
	}
	return false
}

func (a *ClaudeAdapter) GenerateGlobalContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	path, err := homePath(".claude", "CLAUDE.md")
	if err != nil {
		return err
	}
	return upsertManagedBlock(path, buildGlobalContext(a.Watermark(), config, constraints, skills, identity))
}

func (a *ClaudeAdapter) RegisterGlobalMCP() error {
	return upsertClaudeUserMCP()
}

func (a *ClaudeAdapter) RegisterGlobalHooks() error {
	settingsPath, err := homePath(".claude", "settings.json")
	if err != nil {
		return err
	}
	claudeDir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}
	settings := make(map[string]any)
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parsing settings.json: %w", err)
		}
	}
	settings["hooks"] = claudeHookSettings()
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(settingsPath, data, 0644)
}

// RegisterMCP writes or updates .mcp.json at the project root, injecting the
// MOM MCP server entry. Existing entries for other servers are preserved.
func (a *ClaudeAdapter) RegisterMCP() error {
	mcpPath := filepath.Join(a.projectRoot, ".mcp.json")

	// Load existing .mcp.json or start fresh.
	root := make(map[string]any)
	if data, err := os.ReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing .mcp.json: %w", err)
		}
	}

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}

	servers["mom"] = map[string]any{
		"command": resolveCommand(),
		"args":    []string{"serve", "mcp"},
	}
	root["mcpServers"] = servers

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling .mcp.json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(mcpPath, data, 0644); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}

	return nil
}

func (a *ClaudeAdapter) GeneratedFiles() []string {
	return []string{
		filepath.Join(".claude", "CLAUDE.md"),
		filepath.Join(".claude", "settings.json"),
		".mcp.json",
	}
}

func (a *ClaudeAdapter) GeneratedDirs() []string {
	return []string{".claude"}
}

func (a *ClaudeAdapter) Watermark() string {
	return "<!-- Generated by MOM — do not edit manually -->"
}

func (a *ClaudeAdapter) Capabilities() AdapterCapability {
	var cap AdapterCapability
	if err := yaml.Unmarshal(claudeCapabilitiesYAML, &cap); err != nil {
		// Fallback: return minimal capability if YAML is malformed.
		return AdapterCapability{Name: "claude-code", Version: "1.0"}
	}
	return cap
}

func claudeHookSettings() map[string]any {
	hooks := []HookDef{
		{Event: "Stop", Command: "mom draft"},
		{Event: "SessionEnd", Command: "mom draft"},
	}
	hooksMap := make(map[string]any)
	byEvent := make(map[string][]HookDef)
	for _, h := range hooks {
		byEvent[h.Event] = append(byEvent[h.Event], h)
	}
	for event, defs := range byEvent {
		var matcherGroups []map[string]any
		for _, d := range defs {
			entry := map[string]any{
				"type":    "command",
				"command": d.Command,
				"timeout": 10,
			}
			group := map[string]any{
				"hooks": []map[string]any{entry},
			}
			if d.Matcher != "" {
				group["matcher"] = d.Matcher
			}
			matcherGroups = append(matcherGroups, group)
		}
		hooksMap[event] = matcherGroups
	}
	return hooksMap
}

var (
	_ GlobalAdapter       = (*ClaudeAdapter)(nil)
	_ GlobalHookInstaller = (*ClaudeAdapter)(nil)
	_ HookInstaller       = (*ClaudeAdapter)(nil)
	_ TranscriptSource    = (*ClaudeAdapter)(nil)
)

// HasWatermark checks if a file contains the MOM watermark (or the legacy L.E.O. watermark).
func HasWatermark(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "Generated by MOM") || strings.Contains(s, "Generated by L.E.O.")
}

// BackupIfNeeded creates a .bkp copy of a file if it exists and was NOT
// generated by MOM (i.e., it's a user file). Returns true if a backup
// was created.
func BackupIfNeeded(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		return false, nil // file doesn't exist, no backup needed
	}

	if HasWatermark(path) {
		return false, nil // it's ours, overwrite freely
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading file for backup: %w", err)
	}

	bkpPath := path + ".bkp"
	if err := os.WriteFile(bkpPath, data, 0644); err != nil {
		return false, fmt.Errorf("writing backup: %w", err)
	}

	return true, nil
}

// upsertMCPEntry writes or updates the MOM MCP server entry in the JSON file
// at path. Existing entries for other servers are preserved.
func upsertMCPEntry(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	root := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
		}
	}

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}

	servers["mom"] = map[string]any{
		"command": resolveCommand(),
		"args":    []string{"serve", "mcp"},
	}
	root["mcpServers"] = servers

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", filepath.Base(path), err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}
