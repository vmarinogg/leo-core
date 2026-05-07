package cmd

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/adapters/harness"
	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/config"
	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

//go:embed schema.json
var embeddedSchema embed.FS

var runExternalCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Install MOM's global vault and agent integrations",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().String("harnesses", "", "AI harnesses to configure as a comma list (claude,codex,windsurf,pi,all)")
	initCmd.Flags().Bool("force", false, "Overwrite existing global MOM configuration")
	initCmd.Flags().BoolP("no-interactive", "y", false, "Skip the interactive wizard and use defaults/flags")
}

func runInit(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	noInteractive, _ := cmd.Flags().GetBool("no-interactive")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// Run the interactive onboarding wizard unless:
	//   - --no-interactive / -y was passed, OR
	//   - --harnesses was explicitly provided by the user (direct/scripted mode).
	if !noInteractive && !cmd.Flags().Changed("harnesses") {
		result, err := runOnboarding(cmd.InOrStdin(), cmd.OutOrStdout(), cwd)
		if err != nil {
			return err
		}
		installDir := result.InstallDir
		if installDir == "" {
			installDir = cwd
		}
		if err := runInitWithConfig(cmd, installDir, force, result); err != nil {
			return err
		}

		// Cartographer-driven seeding was retired from `mom init` once
		// MOM became global — the central vault aggregates memories
		// from sessions across every project, so per-project bootstrap
		// scanning no longer fits the model. The `mom map` command
		// stays callable (hidden) for users with existing scripts;
		// quality work belongs in the future redesign.
		return nil
	}

	// Non-interactive path: use flags/defaults. MOM's writable vault/config and
	// harness integrations are installed globally; cwd is only registered as the
	// active project for watcher metadata.
	installDir := cwd

	harnessesFlag, _ := cmd.Flags().GetString("harnesses")
	harnesses := parseHarnessList(harnessesFlag)
	if len(harnesses) == 0 {
		harnesses = []string{"claude"}
	}
	harnesses = resolveInitHarnesses(cwd, harnesses)

	defaults := config.Default()
	return runInitWithConfig(cmd, installDir, force, OnboardingResult{
		Harnesses:  harnesses,
		Language:   defaults.User.Language,
		Mode:       defaults.Communication.Mode,
		InstallDir: installDir,
		ScopeLabel: "repo",
	})
}

func parseHarnessList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func resolveInitHarnesses(cwd string, requested []string) []string {
	if len(requested) != 1 || strings.TrimSpace(requested[0]) != "all" {
		return requested
	}
	registry := harness.NewRegistry(cwd)
	detected := registry.DetectAll()
	out := make([]string, 0, len(detected))
	for _, adapter := range detected {
		out = append(out, adapter.Name())
	}
	return out
}

// runInitWithConfig performs central vault setup and global harness integration
// using the resolved configuration from either the wizard or flag defaults. cwd
// is only used as the active project for watcher metadata; the .mom directory is
// always the central vault dir ($HOME/.mom or MOM_VAULT's parent for tests/local
// runs).
func runInitWithConfig(cmd *cobra.Command, cwd string, force bool, result OnboardingResult) error {
	leoDir, err := centralvault.Dir()
	if err != nil {
		return err
	}

	// Check if already initialized.
	alreadyExists := false
	if _, err := os.Stat(leoDir); err == nil {
		if !force {
			alreadyExists = true
		}
	}

	p := ux.NewPrinter(cmd.OutOrStdout())

	// When the central .mom/ already exists: update config with selected
	// harnesses, refresh global integrations, and reinstall daemon — but skip
	// scaffold.
	if alreadyExists {
		return runReinit(cmd, cwd, leoDir, result, p)
	}

	showSpinner := ux.IsTTY(cmd.OutOrStdout())

	// ── Phase 1: Scaffold directories ───────────────────────────────────────
	var scaffoldErr error
	doScaffold := func() {
		dirs := []string{
			leoDir,
			filepath.Join(leoDir, "memory"),
			filepath.Join(leoDir, "skills"),
			filepath.Join(leoDir, "constraints"),
			filepath.Join(leoDir, "logs"),
			filepath.Join(leoDir, "cache"),
		}
		for _, d := range dirs {
			if err := os.MkdirAll(d, 0755); err != nil {
				scaffoldErr = fmt.Errorf("creating %s: %w", d, err)
				return
			}
		}
		v, err := centralvault.Open()
		if err != nil {
			scaffoldErr = err
			return
		}
		if err := v.Close(); err != nil {
			scaffoldErr = err
			return
		}
	}

	doScaffold()
	if scaffoldErr != nil {
		return scaffoldErr
	}

	// ── Phase 2: Write memory structure ──────────────────────────────────────
	registry := harness.NewRegistry(cwd)

	var kbErr error
	doWriteKB := func() {
		// Build harness config from selected harnesses.
		harnessesCfg := make(map[string]config.HarnessConfig)
		for _, rt := range result.Harnesses {
			_, ok := registry.Get(rt)
			if !ok {
				continue
			}
			harnessesCfg[rt] = config.HarnessConfig{Enabled: true}
		}

		// Infer communication.mode from the onboarding mode selection.
		commMode := result.Mode
		if commMode == "" {
			commMode = "concise"
		}

		// Keep the legacy config field stable for existing readers. Storage and
		// integrations are central/global regardless of this value.
		scopeLabel := result.ScopeLabel
		if scopeLabel == "" {
			scopeLabel = "repo"
		}

		// Write config.yaml.
		cfg := config.Config{
			Version:    "1",
			CoreSource: result.CoreSource,
			Scope:      scopeLabel,
			Harnesses:  harnessesCfg,
			User: config.UserConfig{
				Language: result.Language,
			},
			Communication: config.CommunicationConfig{
				Mode: commMode,
			},
			Memory: config.Default().Memory,
		}

		if err := config.Save(leoDir, &cfg); err != nil {
			kbErr = err
			return
		}

		// Write schema.json.
		schemaData, err := embeddedSchema.ReadFile("schema.json")
		if err != nil {
			kbErr = fmt.Errorf("reading embedded schema: %w", err)
			return
		}
		schemaPath := filepath.Join(leoDir, "schema.json")
		if err := os.WriteFile(schemaPath, schemaData, 0644); err != nil {
			kbErr = fmt.Errorf("writing schema: %w", err)
			return
		}

		// Write identity.json.
		identityPath := filepath.Join(leoDir, "identity.json")
		if err := os.WriteFile(identityPath, []byte(defaultIdentity()), 0644); err != nil {
			kbErr = fmt.Errorf("writing identity.json: %w", err)
			return
		}

		if showSpinner {
			time.Sleep(800 * time.Millisecond)
		}
	}

	if showSpinner {
		sp := ux.NewSpinner(os.Stderr)
		sp.Start("Building memory vault")
		doWriteKB()
		sp.Stop()
	} else {
		doWriteKB()
	}
	if kbErr != nil {
		return kbErr
	}

	// Re-load config for harness generation.
	cfg, err := config.Load(leoDir)
	if err != nil {
		return fmt.Errorf("loading config after write: %w", err)
	}

	// ── Phase 3: Generate harness context files ────────────────────────────
	var genErr error
	doGenerate := func() {
		runtimeCfg := buildRuntimeConfig(cfg)

		runtimeConstraints := buildRuntimeConstraints()
		runtimeSkills := buildRuntimeSkills()
		runtimeIdentity := buildRuntimeIdentity()

		// Install global context/tool integration for all selected harnesses.
		for _, rt := range result.Harnesses {
			adapter, ok := registry.Get(rt)
			if !ok {
				continue
			}
			if err := installGlobalHarness(adapter, rt, runtimeCfg, runtimeConstraints, runtimeSkills, runtimeIdentity); err != nil {
				genErr = err
				return
			}
		}

		if showSpinner {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if showSpinner {
		sp := ux.NewSpinner(os.Stderr)
		sp.Start("Generating agent context files")
		doGenerate()
		sp.Stop()
	} else {
		doGenerate()
	}
	if genErr != nil {
		return genErr
	}

	// ── Phase 3.5: Install global skills ────────────────────────────────────
	installGlobalSkills(p, result.Harnesses)

	// ── Phase 4: Register with global watch daemon ──────────────────────────
	if err := ensureGlobalDaemon(cwd, leoDir, result.Harnesses); err != nil {
		p.Warnf("watch daemon: %v", err)
	} else {
		p.Check("Watch daemon installed")
	}

	// ── Telemetry: emit smoke events ────────────────────────────────────────
	startedAt := time.Now().UTC().Format(time.RFC3339)
	emitter := herald.New(leoDir, cfg.Telemetry.TelemetryEnabled())
	emitter.EmitSessionEvent(herald.SessionEvent{
		SessionID: "s-init",
		RepoID:    filepath.Base(cwd),
		Runtime:   cfg.PrimaryRuntime(),
		StartedAt: startedAt,
		Trigger:   "normal",
	})
	emitter.EmitRuntimeHealth(herald.RuntimeHealth{
		Runtime:       cfg.PrimaryRuntime(),
		TS:            time.Now().UTC().Format(time.RFC3339),
		WrapUpSuccess: true,
		LatencyMS:     0,
	})

	// ── Done ────────────────────────────────────────────────────────────────
	p.Blank()
	p.Check("Memory vault ready")
	for _, rt := range result.Harnesses {
		p.Checkf("%s global integration installed", harnessLabel(rt))
	}
	p.Blank()
	p.Textf("MOM is ready. Try /mom-status or run %s.", p.HighlightCmd("mom status"))
	return nil
}

// runReinit handles `mom init` when the central vault already exists. It
// reconciles selected harnesses, refreshes global integrations even when config
// is unchanged, and registers the current cwd with the global watch daemon.
func runReinit(cmd *cobra.Command, cwd, leoDir string, result OnboardingResult, p *ux.Printer) error {
	cfg, err := config.Load(leoDir)
	if err != nil {
		// Corrupt or missing config — fall back to informational message.
		p.Muted("MOM already exists — run with --force to reinitialize from scratch.")
		return nil
	}
	if cfg.Harnesses == nil {
		cfg.Harnesses = make(map[string]config.HarnessConfig)
	}

	// Reconcile harnesses: enable selected, disable unselected.
	selected := make(map[string]bool, len(result.Harnesses))
	for _, rt := range result.Harnesses {
		selected[rt] = true
	}
	changed := false
	for _, rt := range result.Harnesses {
		existing, exists := cfg.Harnesses[rt]
		if !exists || !existing.Enabled {
			cfg.Harnesses[rt] = config.HarnessConfig{Enabled: true}
			changed = true
		}
	}
	for rt, hc := range cfg.Harnesses {
		if !selected[rt] && hc.Enabled {
			cfg.Harnesses[rt] = config.HarnessConfig{Enabled: false}
			changed = true
		}
	}

	if changed {
		if err := config.Save(leoDir, cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
	}

	// Refresh global context/tool integration for all enabled harnesses. This
	// doubles as a repair path when a user deletes one global file and reruns init.
	registry := harness.NewRegistry(cwd)
	runtimeCfg := buildRuntimeConfig(cfg)
	runtimeConstraints := buildRuntimeConstraints()
	runtimeSkills := buildRuntimeSkills()
	runtimeIdentity := buildRuntimeIdentity()
	installed := make([]string, 0, len(cfg.EnabledHarnesses()))

	for _, rt := range cfg.EnabledHarnesses() {
		adapter, ok := registry.Get(rt)
		if !ok {
			continue
		}
		if err := installGlobalHarness(adapter, rt, runtimeCfg, runtimeConstraints, runtimeSkills, runtimeIdentity); err != nil {
			p.Warnf("%s global integration: %v", rt, err)
			continue
		}
		installed = append(installed, rt)
	}

	installGlobalSkills(p, cfg.EnabledHarnesses())

	// Register with global watch daemon (updated harnesses).
	if err := ensureGlobalDaemon(cwd, leoDir, cfg.EnabledHarnesses()); err != nil {
		p.Warnf("watch daemon: %v", err)
	} else {
		p.Check("watch daemon updated")
	}

	p.Blank()
	if changed {
		p.Check("configuration updated")
	} else {
		p.Check("configuration up to date")
	}
	for _, rt := range installed {
		p.Checkf("%s global integration installed", harnessLabel(rt))
	}
	return nil
}

// buildRuntimeConfig converts a config.Config to a harness.Config.
// Autonomy was retired from the persisted config; generated context files still
// include the balanced default so the runtime retains the behavioral directive.
func buildRuntimeConfig(cfg *config.Config) harness.Config {
	commMode := cfg.Communication.Mode
	if commMode == "" {
		commMode = "concise"
	}
	delivery := cfg.Delivery
	if delivery == "" {
		delivery = "mcp"
	}
	return harness.Config{
		Version: cfg.Version,
		User: harness.UserConfig{
			Language:          cfg.User.Language,
			Autonomy:          "balanced",
			CommunicationMode: commMode,
		},
		Delivery: delivery,
	}
}

// buildRuntimeConstraints returns no generated central constraints. Agent behavior
// is delivered through installed skills and compact context files.
func buildRuntimeConstraints() []harness.Constraint {
	return nil
}

// buildRuntimeSkills returns no generated central skills. Slash skills are
// installed through the skills package manager instead.
func buildRuntimeSkills() []harness.Skill {
	return nil
}

// buildRuntimeIdentity parses the identity JSON into a harness.Identity.
func buildRuntimeIdentity() *harness.Identity {
	var identityData struct {
		What        string   `json:"what"`
		Philosophy  string   `json:"philosophy"`
		Constraints []string `json:"constraints"`
	}
	json.Unmarshal([]byte(defaultIdentity()), &identityData) //nolint:errcheck
	return &harness.Identity{
		What:        identityData.What,
		Philosophy:  identityData.Philosophy,
		Constraints: identityData.Constraints,
	}
}

func installGlobalSkills(p *ux.Printer, harnesses []string) {
	for _, h := range harnesses {
		agent, ok := skillsAgentForHarness(h)
		if !ok {
			p.Warnf("skills: unsupported harness %s", h)
			continue
		}
		args, command := skillsInstallCommand(agent)
		if output, err := runExternalCommand("npx", args...); err != nil {
			p.Warnf("skills install %s → %s failed: %v", h, agent, err)
			if len(output) > 0 {
				p.Muted(strings.TrimSpace(string(output)))
			}
			p.Muted(fmt.Sprintf("Retry: mom init --force, or run: %s", command))
			continue
		}
		p.Checkf("skills installed for %s → %s", h, agent)
	}
}

func skillsInstallCommand(agent string) ([]string, string) {
	args := []string{"skills", "add", "momhq/mom", "-g", "-s", "*", "-a", agent, "-y"}
	return args, fmt.Sprintf("npx skills add momhq/mom -g -s '*' -a %s -y", agent)
}

func skillsAgentForHarness(h string) (string, bool) {
	switch h {
	case "claude":
		return "claude-code", true
	case "codex":
		return "codex", true
	case "windsurf":
		return "windsurf", true
	case "pi":
		return "pi", true
	default:
		return "", false
	}
}

func installGlobalHarness(adapter harness.Adapter, rt string, runtimeCfg harness.Config, constraints []harness.Constraint, skills []harness.Skill, identity *harness.Identity) error {
	global, ok := adapter.(harness.GlobalAdapter)
	if !ok {
		return fmt.Errorf("%s does not support global install", rt)
	}
	if err := global.GenerateGlobalContextFile(runtimeCfg, constraints, skills, identity); err != nil {
		return fmt.Errorf("generating context: %w", err)
	}
	if err := global.RegisterGlobalMCP(); err != nil {
		return fmt.Errorf("registering tools: %w", err)
	}
	if h, ok := adapter.(harness.GlobalHookInstaller); ok {
		if err := h.RegisterGlobalHooks(); err != nil {
			return fmt.Errorf("registering hooks: %w", err)
		}
	}
	if e, ok := adapter.(harness.GlobalExtensionInstaller); ok {
		if err := e.RegisterGlobalExtension(); err != nil {
			return fmt.Errorf("registering extension: %w", err)
		}
	}
	return nil
}
