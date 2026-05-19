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

// DefaultTranscriptDir returns Codex's session transcript directory.
// Honors $CODEX_HOME when set (per Codex docs); otherwise falls back to
// ~/.codex/sessions.
func (a *CodexAdapter) DefaultTranscriptDir() string {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "sessions")
	}
	return "~/.codex/sessions"
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
	return writeCodexHooks(filepath.Join(a.projectRoot, ".codex", "hooks.json"))
}

// RegisterGlobalHooks writes the same hook contract to the user-level
// Codex config dir (~/.codex/hooks.json, or $CODEX_HOME/hooks.json when
// set) so Codex Desktop sessions fire `mom watch --sweep` after each
// Cascade response — same defensive sweep wired for project-local
// installs, scoped to the user.
func (a *CodexAdapter) RegisterGlobalHooks() error {
	path, err := codexHomePath("hooks.json")
	if err != nil {
		return err
	}
	return writeCodexHooks(path)
}

// writeCodexHooks renders Codex's hooks.json format at the given path,
// creating parent dirs as needed. The hook set is intentionally small:
// one Stop hook running the resolved MOM binary. Auxiliary signal — the
// daemon's fsnotify watcher catches new transcripts even when this
// hook never fires.
func writeCodexHooks(hooksPath string) error {
	hooks := []HookDef{
		{Event: "Stop", Command: "mom watch --sweep --global"},
	}
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(hooksPath), err)
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

	root := map[string]any{"hooks": byEvent}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling hooks: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(hooksPath, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", hooksPath, err)
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

// codexFeaturesBlock enables Codex hooks. Codex deprecated the old
// `codex_hooks` flag in favour of `hooks`.
const codexFeaturesBlock = `
[features]
hooks = true
`

// upsertCodexMCPEntry ensures [mcp_servers.mom] and [features].hooks exist in
// a Codex config.toml. Idempotent and tolerant of older MOM writes that left
// duplicate [features] tables or the deprecated codex_hooks key.
func upsertCodexMCPEntry(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	// Build the MCP block. Always use the literal "mom" command so brew /
	// npm / package upgrades pick up the right binary at runtime, instead of
	// baking whichever absolute path happened to be on PATH at install time.
	codexMCPBlock := "\n[mcp_servers.mom]\ncommand = \"mom\"\nargs = [\"serve\", \"mcp\"]\n"

	content := string(existing)
	changed := false

	if strings.Contains(content, "[mcp_servers.mom]") {
		refreshed, refreshedChanged := replaceCodexMCPBlock(content)
		if refreshedChanged {
			content = refreshed
			changed = true
		}
	} else {
		content = strings.TrimRight(content, "\n") + "\n" + codexMCPBlock
		changed = true
	}

	cleaned := normalizeCodexFeaturesBlock(content)
	if cleaned != content {
		content = cleaned
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

// replaceCodexMCPBlock rewrites the existing [mcp_servers.mom] section so
// its command/args fields always reflect the canonical "mom serve mcp" entry.
// Stale paths baked by earlier installs (e.g. /tmp/mom-dev) are repaired.
// The block ends at the next top-level [section] header. Any nested
// [mcp_servers.mom.env] table is dropped — it was test-only debris.
func replaceCodexMCPBlock(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	var out []string
	inBlock := false
	changed := false
	canonical := []string{
		"[mcp_servers.mom]",
		"command = \"mom\"",
		"args = [\"serve\", \"mcp\"]",
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
		if trimmed == "[mcp_servers.mom]" {
			inBlock = true
			out = append(out, canonical...)
			changed = true
			continue
		}
		if inBlock {
			if isHeader {
				// nested mcp_servers.mom.* tables (e.g. .env) are dropped.
				if strings.HasPrefix(trimmed, "[mcp_servers.mom.") {
					changed = true
					continue
				}
				inBlock = false
				out = append(out, line)
				continue
			}
			// drop original key/value lines inside the block.
			changed = true
			continue
		}
		out = append(out, line)
	}
	rebuilt := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	return rebuilt, changed && rebuilt != content
}

func normalizeCodexFeaturesBlock(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	inFeatures := false
	skipDuplicateFeatures := false
	featuresSeen := false
	hooksSeen := false
	changed := false

	flushFeatures := func() {
		if inFeatures && !hooksSeen {
			out = append(out, "hooks = true")
			changed = true
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
		if isHeader {
			flushFeatures()
			inFeatures = trimmed == "[features]"
			skipDuplicateFeatures = false
			hooksSeen = false
			if inFeatures {
				if featuresSeen {
					inFeatures = false
					skipDuplicateFeatures = true
					changed = true
					continue
				}
				featuresSeen = true
			}
			out = append(out, line)
			continue
		}

		if skipDuplicateFeatures {
			changed = true
			continue
		}

		if inFeatures {
			if strings.HasPrefix(trimmed, "codex_hooks") {
				if !hooksSeen {
					out = append(out, "hooks = true")
					hooksSeen = true
				}
				changed = true
				continue
			}
			if strings.HasPrefix(trimmed, "hooks") {
				if hooksSeen {
					changed = true
					continue
				}
				out = append(out, "hooks = true")
				hooksSeen = true
				if trimmed != "hooks = true" {
					changed = true
				}
				continue
			}
		}
		out = append(out, line)
	}
	flushFeatures()

	if !featuresSeen {
		trimmed := strings.TrimRight(strings.Join(out, "\n"), "\n")
		if trimmed != "" {
			trimmed += "\n"
		}
		return trimmed + strings.TrimLeft(codexFeaturesBlock, "\n")
	}

	result := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	if !changed && result == content {
		return content
	}
	return result
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
	_ GlobalAdapter       = (*CodexAdapter)(nil)
	_ HookInstaller       = (*CodexAdapter)(nil)
	_ GlobalHookInstaller = (*CodexAdapter)(nil)
	_ TranscriptSource    = (*CodexAdapter)(nil)
)
