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

//go:embed capabilities/codex.yaml
var codexCapabilitiesYAML []byte

// CodexAdapter implements the Adapter interface for OpenAI Codex.
// It reads from .mom/ and generates AGENTS.md at the project root.
type CodexAdapter struct {
	projectRoot string
}

// NewCodexAdapter creates a CodexAdapter for the given project root.
func NewCodexAdapter(projectRoot string) *CodexAdapter {
	return &CodexAdapter{projectRoot: projectRoot}
}

func (a *CodexAdapter) Name() string {
	return "codex"
}

func (a *CodexAdapter) Tier() Tier {
	return Fluent
}

func (a *CodexAdapter) GenerateContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	var body string
	if config.Delivery == "context-file" {
		body = BuildContextContent(config, constraints, skills, identity)
	} else {
		body = BuildMinimalContextContent()
	}
	content := a.Watermark() + "\n\n" + body

	agentsFile := filepath.Join(a.projectRoot, "AGENTS.md")
	if err := os.WriteFile(agentsFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}

	return nil
}

func (a *CodexAdapter) RegisterHooks() error {
	hooks := []HookDef{
		{Event: "Stop", Command: "mom watch --sweep"},
	}
	codexDir := filepath.Join(a.projectRoot, ".codex")
	hooksPath := filepath.Join(codexDir, "hooks.json")

	// Ensure .codex/ exists.
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		return fmt.Errorf("creating .codex dir: %w", err)
	}

	// Codex hooks.json format: { "hooks": { "Event": [ { "hooks": [ {...} ] } ] } }
	byEvent := make(map[string][]map[string]any)
	for _, h := range hooks {
		entry := map[string]any{
			"type":    "command",
			"command": h.Command,
			"timeout": 10,
		}
		group := map[string]any{
			"hooks": []map[string]any{entry},
		}
		if h.Matcher != "" {
			group["matcher"] = h.Matcher
		}
		byEvent[h.Event] = append(byEvent[h.Event], group)
	}

	root := map[string]any{
		"hooks": byEvent,
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling hooks: %w", err)
	}

	data = append(data, '\n')
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		return fmt.Errorf("writing hooks.json: %w", err)
	}

	return nil
}

// RegisterMCP writes MOM's MCP server entry to both the project-level .mcp.json
// (shared with other Harnesses) and Codex's config.toml files (project-level and
// global ~/.codex/config.toml), which is where Codex actually reads MCP config.
func (a *CodexAdapter) RegisterMCP() error {
	// 1. Project-level .mcp.json (shared with other Harnesses).
	mcpPath := filepath.Join(a.projectRoot, ".mcp.json")
	if err := upsertMCPEntry(mcpPath); err != nil {
		return err
	}

	// 2. Project-level .codex/config.toml — Codex reads MCP from here.
	projectConfig := filepath.Join(a.projectRoot, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0755); err != nil {
		return fmt.Errorf("creating .codex dir: %w", err)
	}
	if err := upsertCodexMCPEntry(projectConfig); err != nil {
		return err
	}

	// 3. Global ~/.codex/config.toml — best-effort (non-fatal).
	home, err := os.UserHomeDir()
	if err == nil {
		globalConfig := filepath.Join(home, ".codex", "config.toml")
		if _, err := os.Stat(filepath.Dir(globalConfig)); err == nil {
			_ = upsertCodexMCPEntry(globalConfig)
		}
	}

	return nil
}

// codexFeaturesBlock enables the hooks feature flag required by Codex.
const codexFeaturesBlock = `
[features]
codex_hooks = true
`

// upsertCodexMCPEntry ensures [mcp_servers.mom] and [features] codex_hooks
// exist in a Codex config.toml. Idempotent — skips sections that already exist.
func upsertCodexMCPEntry(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	// Build the MCP block with the resolved absolute path to the mom binary.
	codexMCPBlock := fmt.Sprintf("\n[mcp_servers.mom]\ncommand = %q\nargs = [\"serve\", \"mcp\"]\n", resolveCommand())

	content := string(existing)
	changed := false

	if !strings.Contains(content, "[mcp_servers.mom]") {
		content = strings.TrimRight(content, "\n") + "\n" + codexMCPBlock
		changed = true
	}

	if !strings.Contains(content, "codex_hooks") {
		content = strings.TrimRight(content, "\n") + "\n" + codexFeaturesBlock
		changed = true
	}

	if !changed {
		return nil
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (a *CodexAdapter) DetectHarness() bool {
	if commandExists("codex") {
		return true
	}
	if home := os.Getenv("CODEX_HOME"); home != "" && pathExists(home) {
		return true
	}
	if path, err := homePath(".codex"); err == nil && pathExists(path) {
		return true
	}
	return false
}

func (a *CodexAdapter) GenerateGlobalContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	path, err := codexHomePath("AGENTS.md")
	if err != nil {
		return err
	}
	return upsertManagedBlock(path, buildGlobalContext(a.Watermark(), config, constraints, skills, identity))
}

func (a *CodexAdapter) RegisterGlobalMCP() error {
	path, err := codexHomePath("config.toml")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating .codex dir: %w", err)
	}
	return upsertCodexMCPEntry(path)
}

func (a *CodexAdapter) GeneratedFiles() []string {
	return []string{
		"AGENTS.md",
		filepath.Join(".codex", "hooks.json"),
		filepath.Join(".codex", "config.toml"),
		".mcp.json",
	}
}

func (a *CodexAdapter) GeneratedDirs() []string {
	return []string{".codex"}
}

func (a *CodexAdapter) Watermark() string {
	return "<!-- Generated by MOM — do not edit manually -->"
}

func (a *CodexAdapter) Capabilities() AdapterCapability {
	var cap AdapterCapability
	if err := yaml.Unmarshal(codexCapabilitiesYAML, &cap); err != nil {
		return AdapterCapability{Name: "codex", Version: "0.1"}
	}
	return cap
}

func codexHomePath(parts ...string) (string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		var err error
		home, err = homePath(".codex")
		if err != nil {
			return "", err
		}
	}
	items := append([]string{home}, parts...)
	return filepath.Join(items...), nil
}

var (
	_ GlobalAdapter = (*CodexAdapter)(nil)
	_ HookInstaller = (*CodexAdapter)(nil)
)
