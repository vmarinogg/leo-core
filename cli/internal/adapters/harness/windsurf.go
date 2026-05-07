package harness

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed capabilities/windsurf.yaml
var windsurfCapabilitiesYAML []byte

// WindsurfAdapter implements the Adapter interface for Windsurf.
// It reads from .mom/ and generates .windsurf/rules/mom.md + hooks.json.
type WindsurfAdapter struct {
	projectRoot string
}

// NewWindsurfAdapter creates a WindsurfAdapter for the given project root.
func NewWindsurfAdapter(projectRoot string) *WindsurfAdapter {
	return &WindsurfAdapter{projectRoot: projectRoot}
}

func (a *WindsurfAdapter) Name() string {
	return "windsurf"
}

func (a *WindsurfAdapter) Tier() Tier {
	return Functional
}

func (a *WindsurfAdapter) GenerateContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	rulesDir := filepath.Join(a.projectRoot, ".windsurf", "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		return fmt.Errorf("creating .windsurf/rules dir: %w", err)
	}

	var body string
	if config.Delivery == "context-file" {
		body = BuildContextContent(config, constraints, skills, identity)
	} else {
		body = BuildMinimalContextContent()
	}

	// Windsurf rules require YAML frontmatter
	frontmatter := "---\ntrigger: always_on\n---\n\n"
	content := frontmatter + a.Watermark() + "\n\n" + body

	contextFile := filepath.Join(rulesDir, "mom.md")
	if err := os.WriteFile(contextFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing mom.md: %w", err)
	}
	return nil
}

// DefaultTranscriptDir returns Windsurf's transcript directory.
func (a *WindsurfAdapter) DefaultTranscriptDir() string {
	return "~/.windsurf/transcripts/"
}

func (a *WindsurfAdapter) RegisterHooks() error {
	windsurfDir := filepath.Join(a.projectRoot, ".windsurf")
	if err := os.MkdirAll(windsurfDir, 0755); err != nil {
		return fmt.Errorf("creating .windsurf dir: %w", err)
	}

	hooksPath := filepath.Join(windsurfDir, "hooks.json")

	root := map[string]any{
		"hooks": windsurfHookSettings(a.projectRoot),
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

func (a *WindsurfAdapter) DetectHarness() bool {
	if commandExists("windsurf") {
		return true
	}
	if path, err := homePath(".codeium", "windsurf"); err == nil && pathExists(path) {
		return true
	}
	return false
}

func (a *WindsurfAdapter) GenerateGlobalContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	path, err := homePath(".codeium", "windsurf", "memories", "global_rules.md")
	if err != nil {
		return err
	}
	return upsertManagedBlock(path, buildGlobalContext(a.Watermark(), config, constraints, skills, identity))
}

func (a *WindsurfAdapter) RegisterGlobalMCP() error {
	path, err := homePath(".codeium", "windsurf", "mcp_config.json")
	if err != nil {
		return err
	}
	return upsertMCPEntry(path)
}

func (a *WindsurfAdapter) RegisterGlobalHooks() error {
	path, err := homePath(".codeium", "windsurf", "hooks.json")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating windsurf config dir: %w", err)
	}
	root := map[string]any{
		"hooks": windsurfHookSettings(""),
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling hooks: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// RegisterMCP writes MOM's MCP server entry to both the project-level .mcp.json
// and Windsurf's global config at ~/.codeium/windsurf/mcp_config.json.
//
// Windsurf only reads the global config for MCP servers. The global entry includes
// MOM_PROJECT_DIR so the MCP server resolves the correct scope. In multi-project
// setups the last project to call RegisterMCP wins — run `mom upgrade` in the
// active project to point the global config at it.
func (a *WindsurfAdapter) RegisterMCP() error {
	// 1. Project-level .mcp.json (shared with other Harnesses).
	mcpPath := filepath.Join(a.projectRoot, ".mcp.json")
	if err := upsertMCPEntryWithEnv(mcpPath, a.projectRoot); err != nil {
		return err
	}

	// 2. Windsurf global config — best-effort (non-fatal if dir absent).
	home, err := os.UserHomeDir()
	if err == nil {
		globalConfig := filepath.Join(home, ".codeium", "windsurf", "mcp_config.json")
		if _, err := os.Stat(filepath.Dir(globalConfig)); err == nil {
			if err := upsertMCPEntryWithEnv(globalConfig, a.projectRoot); err != nil {
				return fmt.Errorf("updating windsurf global mcp config: %w", err)
			}
		}
	}

	return nil
}

// upsertMCPEntryWithEnv is like upsertMCPEntry but adds MOM_PROJECT_DIR env var.
// Used by Harnesses that start MCP subprocesses from a different cwd (Windsurf, Cline VS Code).
func upsertMCPEntryWithEnv(path, projectRoot string) error {
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
		"env": map[string]string{
			"MOM_PROJECT_DIR": projectRoot,
		},
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

func windsurfHookSettings(workingDir string) map[string][]map[string]any {
	hooks := []HookDef{
		{Event: "post_cascade_response_with_transcript", Command: "mom watch --sweep"},
	}
	byEvent := make(map[string][]map[string]any)
	for _, h := range hooks {
		entry := map[string]any{"command": h.Command}
		if workingDir != "" {
			entry["working_directory"] = workingDir
		}
		byEvent[h.Event] = append(byEvent[h.Event], entry)
	}
	return byEvent
}

func (a *WindsurfAdapter) GeneratedFiles() []string {
	return []string{
		filepath.Join(".windsurf", "rules", "mom.md"),
		filepath.Join(".windsurf", "hooks.json"),
		".mcp.json",
	}
}

func (a *WindsurfAdapter) GeneratedDirs() []string {
	return []string{".windsurf"}
}

func (a *WindsurfAdapter) Watermark() string {
	return "<!-- Generated by MOM — do not edit manually -->"
}

func (a *WindsurfAdapter) Capabilities() AdapterCapability {
	var cap AdapterCapability
	if err := yaml.Unmarshal(windsurfCapabilitiesYAML, &cap); err != nil {
		// Fallback: return minimal capability if YAML is malformed.
		return AdapterCapability{Name: "windsurf", Version: "1.0"}
	}
	return cap
}

var (
	_ GlobalAdapter       = (*WindsurfAdapter)(nil)
	_ GlobalHookInstaller = (*WindsurfAdapter)(nil)
	_ HookInstaller       = (*WindsurfAdapter)(nil)
	_ TranscriptSource    = (*WindsurfAdapter)(nil)
)
