package harness

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed capabilities/pi.yaml
var piCapabilitiesYAML []byte

// piInstallCommand runs `pi install <pkg>` and returns the combined
// output. Package-level var so tests can inject a stub.
var piInstallCommand = func(pkg string) ([]byte, error) {
	return exec.Command("pi", "install", pkg).CombinedOutput()
}

// commandExistsForPi reports whether the `pi` CLI is on PATH. Same
// seam pattern as piInstallCommand — tests stub it directly.
var commandExistsForPi = func() bool { return commandExists("pi") }

// piMomPackage is the canonical npm package distributed through the Pi
// marketplace. ADR pointer: see closed issue #255 for the design lock
// (delegation over embedding). The package is published to npm and
// auto-listed at https://pi.dev/packages/pi-mom by the `pi-package`
// keyword. Pi installs globally by default — the extension lands at
// ~/.pi/agent/extensions/ and every Pi session sees it.
const piMomPackage = "npm:pi-mom"

// PiAdapter implements the Adapter interface for pi
// (https://github.com/mariozechner/pi), a TypeScript-based coding agent.
//
// Pi reads AGENTS.md at the project root for context (shared with Codex)
// and supports user-level extensions installed via the Pi marketplace.
// `mom init` delegates extension installation to `pi install npm:pi-mom`
// rather than laying down a project-local TypeScript file — the extension
// is maintained as a standalone npm package and updated through `pi update`.
type PiAdapter struct {
	projectRoot string
}

// NewPiAdapter creates a PiAdapter for the given project root.
func NewPiAdapter(projectRoot string) *PiAdapter {
	return &PiAdapter{projectRoot: projectRoot}
}

func (a *PiAdapter) Name() string { return "pi" }

func (a *PiAdapter) Tier() Tier { return Native }

func (a *PiAdapter) GenerateContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	var body string
	if config.Delivery == "context-file" {
		body = BuildContextContent(config, constraints, skills, identity)
	} else {
		body = BuildMinimalContextContent()
	}
	content := a.Watermark() + "\n\n" + body

	// AGENTS.md is shared with Codex; both Harnesses produce identical content
	// from the same .mom/ inputs, so co-installation is a no-op collision.
	agentsFile := filepath.Join(a.projectRoot, "AGENTS.md")
	if err := os.WriteFile(agentsFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing AGENTS.md: %w", err)
	}
	return nil
}

// DefaultTranscriptDir returns pi's session transcript directory.
func (a *PiAdapter) DefaultTranscriptDir() string {
	return "~/.pi/agent/sessions/"
}

func (a *PiAdapter) DetectHarness() bool {
	if commandExists("pi") {
		return true
	}
	if path, err := homePath(".pi", "agent"); err == nil && pathExists(path) {
		return true
	}
	return false
}

func (a *PiAdapter) GenerateGlobalContextFile(config Config, constraints []Constraint, skills []Skill, identity *Identity) error {
	path, err := homePath(".pi", "agent", "AGENTS.md")
	if err != nil {
		return err
	}
	return upsertManagedBlock(path, buildGlobalContext(a.Watermark(), config, constraints, skills, identity))
}

func (a *PiAdapter) RegisterGlobalMCP() error {
	return nil
}

// RegisterGlobalExtension installs the pi-mom extension through the Pi
// marketplace (`pi install npm:pi-mom`). Pi places the extension at
// ~/.pi/agent/extensions/ where every Pi session picks it up; updates
// flow through `pi update`. Per the #255 design lock, MOM no longer
// embeds or writes the TypeScript source — the extension is a
// standalone published artifact.
//
// Requires the `pi` CLI on PATH. The caller (mom init) handles the
// not-installed case by surfacing the install hint to the user.
func (a *PiAdapter) RegisterGlobalExtension() error {
	if !commandExistsForPi() {
		return fmt.Errorf("pi CLI not found on PATH; install pi first, then re-run mom init")
	}
	if out, err := piInstallCommand(piMomPackage); err != nil {
		return fmt.Errorf("pi install %s: %w (output: %s)", piMomPackage, err, string(out))
	}
	return nil
}

// RegisterMCP writes the MOM MCP server entry to the project-level .mcp.json,
// which is the file pi reads for MCP server config (verified empirically:
// pi's MCP gateway picks up servers from <projectRoot>/.mcp.json).
//
// MOM_PROJECT_DIR is set so the MCP server resolves the correct scope when pi
// spawns it from a different cwd (which it does — pi launches MCP children
// from its own working directory, not necessarily the project root).
func (a *PiAdapter) RegisterMCP() error {
	mcpPath := filepath.Join(a.projectRoot, ".mcp.json")
	return upsertMCPEntryWithEnv(mcpPath, a.projectRoot)
}

func (a *PiAdapter) GeneratedFiles() []string {
	// The pi-mom extension is installed by Pi itself into
	// ~/.pi/agent/extensions/ and is not a MOM-generated file from
	// the project's perspective. AGENTS.md and .mcp.json are the only
	// files MOM puts down in the project root.
	return []string{
		"AGENTS.md",
		".mcp.json",
	}
}

func (a *PiAdapter) GeneratedDirs() []string {
	return []string{".pi"}
}

func (a *PiAdapter) Watermark() string {
	return "<!-- Generated by MOM — do not edit manually -->"
}

func (a *PiAdapter) Capabilities() AdapterCapability {
	var cap AdapterCapability
	if err := yaml.Unmarshal(piCapabilitiesYAML, &cap); err != nil {
		return AdapterCapability{Name: "pi", Version: "1.0"}
	}
	return cap
}

var (
	_ GlobalAdapter            = (*PiAdapter)(nil)
	_ GlobalExtensionInstaller = (*PiAdapter)(nil)
	_ TranscriptSource         = (*PiAdapter)(nil)
)

// upsertMCPEntryWithEnv writes the MOM MCP server entry to path while
// preserving any other mcpServers entries. MOM_PROJECT_DIR is set so
// the MCP server resolves the correct scope when pi spawns it from a
// different cwd.
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
		"command": "mom",
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
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return nil
}
