package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/adapters/harness"
	"github.com/momhq/mom/cli/internal/adapters/storage"
	"github.com/momhq/mom/cli/internal/config"
	"github.com/momhq/mom/cli/internal/project"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade .mom/ to the latest version (preserves your memory docs)",
	Long: `Upgrades core infrastructure (schema and harness files)
to match the installed mom binary. Your documents in .mom/memory/ are never touched.

Users on versions older than v0.30 must first upgrade to v0.30 and run
mom upgrade there before upgrading to v0.40 or newer.`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().Bool("dry-run", false, "Show what would change without modifying anything")
}

// upgradeAction tracks a single change for reporting.
type upgradeAction struct {
	symbol string // ✔, ⚠, +
	desc   string
}

func errPreV030Layout(layout string) error {
	return fmt.Errorf("pre-v0.30 layout %s is no longer migrated by this MOM version; upgrade to MOM v0.30 first, run `mom upgrade`, then upgrade to v0.40+", layout)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	momDir, err := findMomDir()
	if err != nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			if _, statErr := os.Stat(filepath.Join(cwd, ".leo")); statErr == nil {
				return errPreV030Layout(".leo/")
			}
		}
		return err
	}

	projectRoot := filepath.Dir(momDir)

	return upgradeSingleDir(cmd, projectRoot, dryRun)
}

// resolveDaemonProjectRoot picks the directory to register with the global
// watch daemon. With the v0.40 central vault, momDir is always ~/.mom and
// filepath.Dir(momDir) resolves to $HOME — never a real project. Fall back
// to the cwd / nearest .mom-project.yaml ancestor so `mom upgrade` registers
// the project the user actually invoked it from.
func resolveDaemonProjectRoot(momDir, fallback string) string {
	home, err := os.UserHomeDir()
	if err != nil || filepath.Clean(momDir) != filepath.Join(home, ".mom") {
		return fallback
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fallback
	}
	if _, source, found, _ := project.ResolveProject(cwd); found && source != "" {
		return filepath.Dir(source)
	}
	return cwd
}

// upgradeSingleDir runs the full upgrade pipeline on a single .mom/ directory.
func upgradeSingleDir(cmd *cobra.Command, projectRoot string, dryRun bool) error {
	momDir := filepath.Join(projectRoot, ".mom")
	showSpinner := ux.IsTTY(cmd.OutOrStdout())

	// Check if this dir has a .mom/ at all.
	if _, err := os.Stat(momDir); os.IsNotExist(err) {
		return nil // not a MOM project, skip silently
	}

	if !isMomProject(momDir) {
		return nil
	}

	var actions []upgradeAction
	addAction := func(symbol, desc string) {
		actions = append(actions, upgradeAction{symbol, desc})
	}

	if _, statErr := os.Stat(filepath.Join(momDir, "kb")); statErr == nil {
		return errPreV030Layout(".mom/kb/")
	}

	// ── Phase 1: Load, migrate, and persist config ───────────────────────────
	cfg, err := config.Load(momDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	pruneRetiredHarnesses(cfg, addAction)

	var phase1Err error
	doPhase1 := func() {
		if cfg.Communication.Mode == "" {
			cfg.Communication.Mode = "concise"
			addAction("✔", "communication.mode set to concise (default)")
		}

		if err := config.Save(momDir, cfg); err != nil {
			phase1Err = fmt.Errorf("saving config: %w", err)
			return
		}
		addAction("✔", "config.yaml migrated to latest format")

		if scrubbed, changed, err := scrubDeadConfigFields(momDir); err != nil {
			phase1Err = fmt.Errorf("scrubbing dead config fields: %w", err)
			return
		} else if changed {
			if !dryRun {
				configPath := filepath.Join(momDir, "config.yaml")
				if err := os.WriteFile(configPath, scrubbed, 0644); err != nil {
					phase1Err = fmt.Errorf("writing scrubbed config: %w", err)
					return
				}
			}
			addAction("✔", "removed retired fields (tiers, autonomy) from config.yaml")
		}

		newDirs := []string{
			filepath.Join(momDir, "memory"),
			filepath.Join(momDir, "constraints"),
			filepath.Join(momDir, "skills"),
			filepath.Join(momDir, "logs"),
			filepath.Join(momDir, "cache"),
			filepath.Join(momDir, "raw"),
		}
		for _, d := range newDirs {
			if _, err := os.Stat(d); os.IsNotExist(err) {
				if !dryRun {
					if err := os.MkdirAll(d, 0755); err != nil {
						phase1Err = fmt.Errorf("creating %s: %w", d, err)
						return
					}
				}
				rel, _ := filepath.Rel(projectRoot, d)
				addAction("+", fmt.Sprintf("created directory %s", rel))
			}
		}

		profilesDir := filepath.Join(momDir, "profiles")
		if _, statErr := os.Stat(profilesDir); statErr == nil {
			if !dryRun {
				if err := os.RemoveAll(profilesDir); err != nil {
					phase1Err = fmt.Errorf("removing profiles/: %w", err)
					return
				}
			}
			addAction("✔", "profiles/ directory removed (retired legacy layout)")
		}

		retiredConstraints := []string{
			"delegation-mandatory",
			"think-before-execute",
			"know-what-you-dont-know",
			"peer-review-automatic",
			"token-economy-caveman",
			"token-economy-model-selection",
			"evidence-over-claim",
			"inheritance",
			"metrics-collection",
			"propagation",
		}
		constraintsDir := filepath.Join(momDir, "constraints")
		for _, name := range retiredConstraints {
			path := filepath.Join(constraintsDir, name+".json")
			if _, statErr := os.Stat(path); statErr == nil {
				if !dryRun {
					if err := os.Remove(path); err != nil {
						phase1Err = fmt.Errorf("removing retired constraint %s: %w", name, err)
						return
					}
				}
				addAction("✔", fmt.Sprintf("retired constraint %s removed", name))
			}
		}

		retiredSkills := []string{"task-intake"}
		skillsDir := filepath.Join(momDir, "skills")
		for _, name := range retiredSkills {
			path := filepath.Join(skillsDir, name+".json")
			if _, statErr := os.Stat(path); statErr == nil {
				if !dryRun {
					if err := os.Remove(path); err != nil {
						phase1Err = fmt.Errorf("removing retired skill %s: %w", name, err)
						return
					}
				}
				addAction("✔", fmt.Sprintf("retired skill %s removed", name))
			}
		}

		removedGenerated, err := removeKnownGeneratedCentralDocs(momDir, dryRun)
		if err != nil {
			phase1Err = err
			return
		}
		for _, action := range removedGenerated {
			addAction(action.symbol, action.desc)
		}

		cleanedHooks, err := removeDeadHookCommands(projectRoot, dryRun)
		if err != nil {
			phase1Err = err
			return
		}
		for _, action := range cleanedHooks {
			addAction(action.symbol, action.desc)
		}

		if showSpinner {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if showSpinner {
		sp := ux.NewSpinner(os.Stderr)
		sp.Start("Checking configuration")
		doPhase1()
		sp.Stop()
	} else {
		doPhase1()
	}
	if phase1Err != nil {
		return phase1Err
	}

	// ── Phase 2: Update core files ─────────────────────────────────────────────
	var phase2Err error
	doPhase2 := func() {
		schemaData, err := embeddedSchema.ReadFile("schema.json")
		if err != nil {
			phase2Err = fmt.Errorf("reading embedded schema: %w", err)
			return
		}
		schemaPath := filepath.Join(momDir, "schema.json")
		if changed := fileChanged(schemaPath, schemaData); changed {
			if !dryRun {
				if err := os.WriteFile(schemaPath, schemaData, 0644); err != nil {
					phase2Err = fmt.Errorf("writing schema: %w", err)
					return
				}
			}
			addAction("✔", "schema.json updated")
		}

		identityPath := filepath.Join(momDir, "identity.json")
		identityBytes := []byte(defaultIdentity())
		if changed := fileChanged(identityPath, identityBytes); changed {
			if !dryRun {
				if err := os.WriteFile(identityPath, identityBytes, 0644); err != nil {
					phase2Err = fmt.Errorf("writing identity.json: %w", err)
					return
				}
			}
			addAction("✔", "identity.json updated")
		}

		docsDir := filepath.Join(momDir, "memory")
		migrated := migrateMetricDocs(docsDir, dryRun)
		for _, docID := range migrated {
			addAction("✔", fmt.Sprintf("doc %s migrated metric → session-log", docID))
		}

		migratedPatterns := migrateFactASTDocs(docsDir, dryRun)
		for _, docID := range migratedPatterns {
			addAction("✔", fmt.Sprintf("doc %s migrated fact → pattern", docID))
		}

		// Sanitize tags: convert underscores to hyphens, ensure non-empty.
		sanitizedTags := sanitizeDocTags(docsDir, dryRun)
		for _, docID := range sanitizedTags {
			addAction("✔", fmt.Sprintf("doc %s tags sanitized to kebab-case", docID))
		}

		if showSpinner {
			time.Sleep(700 * time.Millisecond)
		}
	}

	if showSpinner {
		sp := ux.NewSpinner(os.Stderr)
		sp.Start("Updating memory structure")
		doPhase2()
		sp.Stop()
	} else {
		doPhase2()
	}
	if phase2Err != nil {
		return phase2Err
	}

	// ── Phase 3: Rebuild index and regenerate harness files ─────────────────
	var phase3Err error
	doPhase3 := func() {
		if !dryRun {
			if err := regenerateHarnessFiles(projectRoot, momDir, cfg); err != nil {
				phase3Err = err
				return
			}
		}
		for _, rt := range cfg.EnabledHarnesses() {
			addAction("✔", fmt.Sprintf("harness %s context file regenerated", rt))
		}

		installSkillsDuringUpgrade(cfg.EnabledHarnesses(), dryRun, addAction)

		// Refresh harness-native extensions (currently just Pi). Skills.sh
		// keeps SKILL.md in sync for every harness; pi additionally ships
		// the deeper pi-mom extension via the Pi marketplace, so we must
		// refresh that on every upgrade or the two sources drift.
		if !dryRun {
			refreshHarnessExtensionsDuringUpgrade(cfg.EnabledHarnesses(), projectRoot, addAction)
		}

		// Rebuild SQLite search index from JSON files.
		if !dryRun {
			idx := storage.NewIndexedAdapter(momDir)
			if err := idx.Reindex(); err != nil {
				addAction("⚠", fmt.Sprintf("reindex: %v", err))
			} else {
				addAction("✔", "SQLite search index rebuilt")
			}
			_ = idx.Close()
		}

		if showSpinner {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if showSpinner {
		sp := ux.NewSpinner(os.Stderr)
		sp.Start("Regenerating harness files")
		doPhase3()
		sp.Stop()
	} else {
		doPhase3()
	}
	if phase3Err != nil {
		return phase3Err
	}

	// ── Phase 3.5: Register with global watch daemon ────────────────────────
	daemonProjectRoot := resolveDaemonProjectRoot(momDir, projectRoot)
	if !dryRun {
		if err := ensureGlobalDaemon(daemonProjectRoot, momDir, cfg.EnabledHarnesses()); err != nil {
			addAction("⚠", fmt.Sprintf("watch daemon: %v", err))
		} else {
			addAction("✔", "watch daemon installed/updated")
		}
	}

	// ── Report ──────────────────────────────────────────────────────────────
	home, _ := os.UserHomeDir()
	display := projectRoot
	if strings.HasPrefix(display, home) {
		display = "~" + display[len(home):]
	}

	p := ux.NewPrinter(cmd.OutOrStdout())
	p.Blank()
	if dryRun {
		p.Bold(fmt.Sprintf("[%s] Dry run — no changes made. Would apply:", display))
	} else {
		p.Bold(fmt.Sprintf("[%s] Upgrade complete:", display))
	}
	p.Blank()
	for _, a := range actions {
		switch a.symbol {
		case "✔":
			p.Check(a.desc)
		case "⚠":
			p.Warn(a.desc)
		case "+":
			p.Check(a.desc)
		default:
			p.Check(a.desc)
		}
	}
	if len(actions) == 0 {
		p.Muted("Everything is already up to date.")
	}
	p.Blank()

	return nil
}

// refreshHarnessExtensionsDuringUpgrade re-runs GlobalExtensionInstaller for
// every enabled harness that implements it. Today this means re-running
// `pi install npm:pi-mom` so the deeper Pi extension stays in lockstep with
// the skills.sh-installed SKILL.md files.
func refreshHarnessExtensionsDuringUpgrade(harnesses []string, projectRoot string, addAction func(string, string)) {
	reg := harness.NewRegistry(projectRoot)
	for _, h := range harnesses {
		adapter, ok := reg.Get(h)
		if !ok {
			continue
		}
		ext, ok := adapter.(harness.GlobalExtensionInstaller)
		if !ok {
			continue
		}
		if err := ext.RegisterGlobalExtension(); err != nil {
			addAction("⚠", fmt.Sprintf("%s extension refresh: %v", h, err))
			continue
		}
		addAction("✔", fmt.Sprintf("%s extension refreshed", h))
	}
}

func installSkillsDuringUpgrade(harnesses []string, dryRun bool, addAction func(string, string)) {
	for _, h := range harnesses {
		agent, ok := skillsAgentForHarness(h)
		if !ok {
			// Pi installs its skills via the pi-mom extension; other
			// harnesses not supported by skills.sh stay silent.
			continue
		}
		args, command := skillsInstallCommand(agent)
		if dryRun {
			addAction("+", "would run "+command)
			continue
		}
		if _, err := runExternalCommand("npx", args...); err != nil {
			detail := fmt.Sprintf("skills install %s → %s failed: %v", h, agent, err)
			detail += fmt.Sprintf("; retry with mom upgrade, mom init --force, or run: %s", command)
			addAction("⚠", detail)
			continue
		}
		addAction("✔", fmt.Sprintf("skills installed for %s → %s", h, agent))
	}
}

// fileChanged returns true if the file at path doesn't exist or differs from data.
func fileChanged(path string, data []byte) bool {
	existing, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	return string(existing) != string(data)
}

func removeKnownGeneratedCentralDocs(momDir string, dryRun bool) ([]upgradeAction, error) {
	var actions []upgradeAction
	for _, doc := range knownGeneratedCentralDocs {
		path := filepath.Join(momDir, doc.DirName, doc.Name+".json")
		if _, statErr := os.Stat(path); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, fmt.Errorf("checking generated %s %s: %w", doc.Kind, doc.Name, statErr)
		}
		if !dryRun {
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("removing generated %s %s: %w", doc.Kind, doc.Name, err)
			}
		}
		actions = append(actions, upgradeAction{"✔", fmt.Sprintf("generated %s %s removed", doc.Kind, doc.Name)})
	}
	return actions, nil
}

func removeDeadHookCommands(projectRoot string, dryRun bool) ([]upgradeAction, error) {
	// Windsurf paths remain here purely for legacy upgrade cleanup —
	// users who installed MOM hooks before Windsurf retirement
	// (#342/#343) still have stale hook entries that need pruning. The
	// harness itself is no longer supported (see harness_retirement.go).
	paths := []string{
		filepath.Join(projectRoot, ".claude", "settings.json"),
		filepath.Join(projectRoot, ".codex", "hooks.json"),
		filepath.Join(projectRoot, ".windsurf", "hooks.json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".claude", "settings.json"),
			filepath.Join(home, ".codex", "hooks.json"),
			filepath.Join(home, ".codeium", "windsurf", "hooks.json"),
		)
	}

	seen := map[string]bool{}
	var actions []upgradeAction
	for _, path := range paths {
		clean := filepath.Clean(path)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		changed, err := removeDeadHookCommandsFromFile(clean, dryRun)
		if err != nil {
			actions = append(actions, upgradeAction{"⚠", fmt.Sprintf("dead hook cleanup skipped for %s: %v", clean, err)})
			continue
		}
		if changed {
			actions = append(actions, upgradeAction{"✔", fmt.Sprintf("dead hook entries removed from %s", clean)})
		}
	}
	return actions, nil
}

func removeDeadHookCommandsFromFile(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading hook config %s: %w", path, err)
	}
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return false, fmt.Errorf("parsing hook config %s: %w", path, err)
	}
	cleaned, keep, changed := stripDeadHookEntries(root)
	if !keep {
		cleaned = map[string]any{}
	}
	if !changed {
		return false, nil
	}
	if dryRun {
		return true, nil
	}
	out, err := json.MarshalIndent(cleaned, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshaling hook config %s: %w", path, err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0644); err != nil {
		return false, fmt.Errorf("writing hook config %s: %w", path, err)
	}
	return true, nil
}

func stripDeadHookEntries(v any) (any, bool, bool) {
	switch x := v.(type) {
	case map[string]any:
		if command, ok := x["command"].(string); ok && isDeadHookCommand(command) {
			return nil, false, true
		}
		changed := false
		for k, child := range x {
			cleaned, keep, childChanged := stripDeadHookEntries(child)
			if childChanged {
				changed = true
			}
			if keep {
				x[k] = cleaned
			} else {
				delete(x, k)
			}
		}
		return x, true, changed
	case []any:
		out := make([]any, 0, len(x))
		changed := false
		for _, child := range x {
			cleaned, keep, childChanged := stripDeadHookEntries(child)
			if childChanged {
				changed = true
			}
			if keep {
				out = append(out, cleaned)
			}
		}
		if len(out) != len(x) {
			changed = true
		}
		return out, true, changed
	default:
		return v, true, false
	}
}

func isDeadHookCommand(command string) bool {
	fields := strings.Fields(command)
	return len(fields) >= 2 && fields[0] == "mom" && (fields[1] == "draft" || fields[1] == "record")
}

// scrubDeadConfigFields reads config.yaml from momDir, removes the retired
// "tiers" key from every harness block and the "autonomy" key from the user
// block, and returns the cleaned bytes plus a changed flag. It does nothing
// when the keys are already absent.
//
// The scrub operates on the raw YAML node tree so that comments and
// formatting are preserved as much as possible.
func scrubDeadConfigFields(momDir string) (scrubbed []byte, changed bool, err error) {
	configPath := filepath.Join(momDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, false, fmt.Errorf("reading config: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, false, fmt.Errorf("parsing config: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return data, false, nil
	}

	doc := root.Content[0] // document node wraps a mapping node
	if doc.Kind != yaml.MappingNode {
		return data, false, nil
	}

	changed = renameYAMLKey(doc, "runtimes", "harnesses") || changed

	// Strip retired "tiers" sub-keys from each harness block.
	if harnessesNode := findMappingValue(doc, "harnesses"); harnessesNode != nil && harnessesNode.Kind == yaml.MappingNode {
		for i := 1; i < len(harnessesNode.Content); i += 2 {
			rtVal := harnessesNode.Content[i]
			if rtVal.Kind == yaml.MappingNode {
				if removeYAMLKey(rtVal, "tiers") {
					changed = true
				}
			}
		}
	}

	if removeYAMLKey(findMappingValue(doc, "user"), "autonomy") {
		changed = true
	}

	if !changed {
		return data, false, nil
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, false, fmt.Errorf("marshaling scrubbed config: %w", err)
	}
	return out, true, nil
}

// renameYAMLKey renames a top-level key in a YAML mapping node from oldKey to
// newKey, preserving the value and its position. Returns true if the key was
// found and renamed.
func renameYAMLKey(mapping *yaml.Node, oldKey, newKey string) bool {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == oldKey {
			mapping.Content[i].Value = newKey
			return true
		}
	}
	return false
}

// findMappingValue returns the value node for key in a YAML mapping node, or nil.
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// removeYAMLKey removes the key+value pair for key from a YAML mapping node.
// Returns true if the key was present and removed.
func removeYAMLKey(mapping *yaml.Node, key string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return true
		}
	}
	return false
}
func migrateMetricDocs(docsDir string, dryRun bool) []string {
	var migrated []string

	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(docsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var doc map[string]interface{}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}

		docType, ok := doc["type"].(string)
		if !ok || docType != "metric" {
			continue
		}

		docID, _ := doc["id"].(string)
		if !dryRun {
			doc["type"] = "session-log"
			updated, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				continue
			}
			os.WriteFile(path, append(updated, '\n'), 0644) //nolint:errcheck
		}
		migrated = append(migrated, docID)
	}

	return migrated
}

// migrateFactASTDocs finds docs with type "fact" that carry an "ast" or "bootstrap"
// tag (written by the cartographer before pattern was a first-class type) and
// converts them to type "pattern". Plain fact docs without those tags are untouched.
func migrateFactASTDocs(docsDir string, dryRun bool) []string {
	var migrated []string

	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(docsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var doc map[string]interface{}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}

		docType, ok := doc["type"].(string)
		if !ok || docType != "fact" {
			continue
		}

		tags, _ := doc["tags"].([]interface{})
		hasASTTag := false
		for _, tag := range tags {
			if s, ok := tag.(string); ok && (s == "ast" || s == "bootstrap") {
				hasASTTag = true
				break
			}
		}
		if !hasASTTag {
			continue
		}

		docID, _ := doc["id"].(string)
		if !dryRun {
			doc["type"] = "pattern"
			updated, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				continue
			}
			os.WriteFile(path, append(updated, '\n'), 0644) //nolint:errcheck
		}
		migrated = append(migrated, docID)
	}

	return migrated
}

// sanitizeDocTags scans memory docs and fixes tags that don't pass kebab-case
// validation: underscores → hyphens, empty tags removed, empty arrays get "untagged".
func sanitizeDocTags(docsDir string, dryRun bool) []string {
	var fixed []string

	entries, err := os.ReadDir(docsDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(docsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var doc map[string]interface{}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}

		rawTags, _ := doc["tags"].([]interface{})
		changed := false
		var newTags []string

		for _, raw := range rawTags {
			tag, ok := raw.(string)
			if !ok || tag == "" {
				changed = true
				continue
			}
			sanitized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(tag)), "_", "-")
			// Remove colons and other non-kebab characters.
			sanitized = kebabOnly(sanitized)
			if sanitized == "" {
				changed = true
				continue
			}
			if sanitized != tag {
				changed = true
			}
			newTags = append(newTags, sanitized)
		}

		if len(newTags) == 0 && len(rawTags) > 0 {
			newTags = []string{"untagged"}
			changed = true
		}
		if len(rawTags) == 0 {
			newTags = []string{"untagged"}
			changed = true
		}

		if !changed {
			continue
		}

		docID, _ := doc["id"].(string)
		if !dryRun {
			// Convert back to []interface{} for JSON.
			tagIface := make([]interface{}, len(newTags))
			for i, t := range newTags {
				tagIface[i] = t
			}
			doc["tags"] = tagIface
			updated, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				continue
			}
			os.WriteFile(path, append(updated, '\n'), 0644) //nolint:errcheck
		}
		fixed = append(fixed, docID)
	}

	return fixed
}

// kebabOnly strips any characters that aren't lowercase alphanumeric or hyphens.
func kebabOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	// Trim leading/trailing hyphens and collapse runs.
	result := b.String()
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	result = strings.Trim(result, "-")
	return result
}

// regenerateHarnessFiles rebuilds all harness context files from the current config.
func regenerateHarnessFiles(projectRoot, momDir string, cfg *config.Config) error {
	registry := harness.NewRegistry(projectRoot)

	harnessCfg := buildHarnessConfig(cfg)
	harnessConstraints := buildHarnessConstraints()
	harnessSkills := buildHarnessSkills()
	harnessIdentity := buildHarnessIdentity()

	for _, rt := range cfg.EnabledHarnesses() {
		adapter, ok := registry.Get(rt)
		if !ok {
			continue
		}
		if err := adapter.GenerateContextFile(harnessCfg, harnessConstraints, harnessSkills, harnessIdentity); err != nil {
			return fmt.Errorf("generating %s context: %w", rt, err)
		}

		if err := adapter.RegisterMCP(); err != nil {
			return fmt.Errorf("registering %s MCP config: %w", rt, err)
		}
		if h, ok := adapter.(harness.HookInstaller); ok {
			if err := h.RegisterHooks(); err != nil {
				return fmt.Errorf("registering %s hooks: %w", rt, err)
			}
		}
	}

	return nil
}
