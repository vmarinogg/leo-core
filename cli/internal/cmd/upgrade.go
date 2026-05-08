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
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade .mom/ to the latest version (preserves your memory docs)",
	Long: `Upgrades core infrastructure (schema and harness files)
to match the installed mom binary. Your documents in .mom/memory/ are never touched.

Legacy central import scans $HOME and asks before writing.`,
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

func runUpgrade(cmd *cobra.Command, args []string) error {
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	momDir, err := findMomDir()
	if err != nil {
		return err
	}

	projectRoot := filepath.Dir(momDir)

	// Upgrade the root scope.
	if err := upgradeSingleDir(cmd, projectRoot, dryRun); err != nil {
		return err
	}

	return runCentralImport(cmd, dryRun)
}

// upgradeSingleDir runs the full upgrade pipeline on a single .mom/ directory.
func upgradeSingleDir(cmd *cobra.Command, projectRoot string, dryRun bool) error {
	momDir := filepath.Join(projectRoot, ".mom")
	showSpinner := ux.IsTTY(cmd.OutOrStdout())

	// Check if this dir has a .mom/ at all.
	if _, err := os.Stat(momDir); os.IsNotExist(err) {
		// Try .leo/ fallback.
		leoDir := filepath.Join(projectRoot, ".leo")
		if _, err := os.Stat(leoDir); os.IsNotExist(err) {
			return nil // not a MOM project, skip silently
		}
		momDir = leoDir
	}

	if !isMomProject(momDir) {
		return nil
	}

	var actions []upgradeAction
	addAction := func(symbol, desc string) {
		actions = append(actions, upgradeAction{symbol, desc})
	}

	// ── Phase -1: Migrate .leo/ → .mom/ legacy path ─────────────────────────
	isLegacyLeoDir := filepath.Base(momDir) == ".leo"
	if isLegacyLeoDir {
		if dryRun {
			addAction("⚠", fmt.Sprintf("would migrate %s → %s (run without --dry-run)", momDir, filepath.Join(projectRoot, ".mom")))
		} else {
			pathActions, err := migrateLeoToMom(momDir)
			if err != nil {
				return fmt.Errorf("path migration: %w", err)
			}
			for _, a := range pathActions {
				addAction(a.symbol, a.desc)
			}
			momDir = filepath.Join(projectRoot, ".mom")
		}
	}

	leoDir := momDir

	// ── Phase 0: Migrate legacy kb/ layout to flat layout ───────────────────
	if !dryRun {
		layoutActions, err := migrateKBLayout(leoDir)
		if err != nil {
			return fmt.Errorf("migrating layout: %w", err)
		}
		for _, a := range layoutActions {
			addAction(a.symbol, a.desc)
		}
	} else {
		if _, statErr := os.Stat(filepath.Join(leoDir, "kb")); statErr == nil {
			addAction("⚠", "legacy .mom/kb/ detected — would flatten to new layout (run without --dry-run)")
		}
	}

	// ── Phase 1: Load, migrate, and persist config ───────────────────────────
	cfg, err := config.Load(leoDir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var phase1Err error
	doPhase1 := func() {
		if cfg.Communication.Mode == "" {
			cfg.Communication.Mode = "concise"
			addAction("✔", "communication.mode set to concise (default)")
		}

		if err := config.Save(leoDir, cfg); err != nil {
			phase1Err = fmt.Errorf("saving config: %w", err)
			return
		}
		addAction("✔", "config.yaml migrated to latest format")

		if scrubbed, changed, err := scrubDeadConfigFields(leoDir); err != nil {
			phase1Err = fmt.Errorf("scrubbing dead config fields: %w", err)
			return
		} else if changed {
			if !dryRun {
				configPath := filepath.Join(leoDir, "config.yaml")
				if err := os.WriteFile(configPath, scrubbed, 0644); err != nil {
					phase1Err = fmt.Errorf("writing scrubbed config: %w", err)
					return
				}
			}
			addAction("✔", "removed retired fields (tiers, autonomy) from config.yaml")
		}

		newDirs := []string{
			filepath.Join(leoDir, "memory"),
			filepath.Join(leoDir, "constraints"),
			filepath.Join(leoDir, "skills"),
			filepath.Join(leoDir, "logs"),
			filepath.Join(leoDir, "cache"),
			filepath.Join(leoDir, "raw"),
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

		profilesDir := filepath.Join(leoDir, "profiles")
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
		constraintsDir := filepath.Join(leoDir, "constraints")
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
		skillsDir := filepath.Join(leoDir, "skills")
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

		removedGenerated, err := removeKnownGeneratedCentralDocs(leoDir, dryRun)
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
		schemaPath := filepath.Join(leoDir, "schema.json")
		if changed := fileChanged(schemaPath, schemaData); changed {
			if !dryRun {
				if err := os.WriteFile(schemaPath, schemaData, 0644); err != nil {
					phase2Err = fmt.Errorf("writing schema: %w", err)
					return
				}
			}
			addAction("✔", "schema.json updated")
		}

		identityPath := filepath.Join(leoDir, "identity.json")
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

		docsDir := filepath.Join(leoDir, "memory")
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
			if err := regenerateHarnessFiles(projectRoot, leoDir, cfg); err != nil {
				phase3Err = err
				return
			}
		}
		for _, rt := range cfg.EnabledHarnesses() {
			addAction("✔", fmt.Sprintf("harness %s context file regenerated", rt))
		}

		installSkillsDuringUpgrade(cfg.EnabledHarnesses(), dryRun, addAction)

		// Rebuild SQLite search index from JSON files.
		if !dryRun {
			idx := storage.NewIndexedAdapter(leoDir)
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
	if !dryRun {
		if err := ensureGlobalDaemon(projectRoot, leoDir, cfg.EnabledHarnesses()); err != nil {
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

func installSkillsDuringUpgrade(harnesses []string, dryRun bool, addAction func(string, string)) {
	for _, h := range harnesses {
		agent, ok := skillsAgentForHarness(h)
		if !ok {
			addAction("⚠", fmt.Sprintf("skills: unsupported harness %s", h))
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

func removeKnownGeneratedCentralDocs(leoDir string, dryRun bool) ([]upgradeAction, error) {
	var actions []upgradeAction
	for _, doc := range knownGeneratedCentralDocs {
		path := filepath.Join(leoDir, doc.DirName, doc.Name+".json")
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

// migrateKBLayout detects a legacy .mom/kb/ layout and promotes each subdirectory
// one level up to the new flat layout. It is idempotent: if the destination already
// exists it skips that step and reports a conflict rather than overwriting.
func migrateKBLayout(leoDir string) ([]upgradeAction, error) {
	kbDir := filepath.Join(leoDir, "kb")
	if _, err := os.Stat(kbDir); os.IsNotExist(err) {
		return nil, nil
	}

	var actions []upgradeAction

	type migration struct {
		src  string
		dst  string
		desc string
	}

	moves := []migration{
		{filepath.Join(kbDir, "docs"), filepath.Join(leoDir, "memory"), "kb/docs/ → memory/"},
		{filepath.Join(kbDir, "constraints"), filepath.Join(leoDir, "constraints"), "kb/constraints/ → constraints/"},
		{filepath.Join(kbDir, "skills"), filepath.Join(leoDir, "skills"), "kb/skills/ → skills/"},
		{filepath.Join(kbDir, "logs"), filepath.Join(leoDir, "logs"), "kb/logs/ → logs/"},
	}

	for _, m := range moves {
		if _, err := os.Stat(m.src); os.IsNotExist(err) {
			continue
		}
		if _, err := os.Stat(m.dst); err == nil {
			actions = append(actions, upgradeAction{"⚠", fmt.Sprintf("skipped %s — destination already exists", m.desc)})
			continue
		}
		if err := os.Rename(m.src, m.dst); err != nil {
			return nil, fmt.Errorf("moving %s: %w", m.desc, err)
		}
		actions = append(actions, upgradeAction{"✔", fmt.Sprintf("migrated %s", m.desc)})
	}

	fileMovs := []migration{
		{filepath.Join(kbDir, "index.json"), filepath.Join(leoDir, "index.json"), "kb/index.json → index.json"},
		{filepath.Join(kbDir, "schema.json"), filepath.Join(leoDir, "schema.json"), "kb/schema.json → schema.json"},
	}
	for _, m := range fileMovs {
		if _, err := os.Stat(m.src); os.IsNotExist(err) {
			continue
		}
		if _, err := os.Stat(m.dst); err == nil {
			actions = append(actions, upgradeAction{"⚠", fmt.Sprintf("skipped %s — destination already exists", m.desc)})
			continue
		}
		if err := os.Rename(m.src, m.dst); err != nil {
			return nil, fmt.Errorf("moving %s: %w", m.desc, err)
		}
		actions = append(actions, upgradeAction{"✔", fmt.Sprintf("migrated %s", m.desc)})
	}

	remaining, _ := os.ReadDir(kbDir)
	hasVisible := false
	for _, e := range remaining {
		if !strings.HasPrefix(e.Name(), ".") {
			hasVisible = true
			break
		}
	}
	if !hasVisible {
		if err := os.RemoveAll(kbDir); err != nil {
			actions = append(actions, upgradeAction{"⚠", fmt.Sprintf("could not remove empty kb/: %v", err)})
		} else {
			actions = append(actions, upgradeAction{"✔", "removed empty kb/ directory"})
		}
	}

	if len(actions) > 0 {
		actions = append([]upgradeAction{{"✔", "filesystem layout migrated (kb/ flattened)"}}, actions...)
	}

	return actions, nil
}

// migrateMetricDocs finds docs with type "metric" and migrates them to "session-log".
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
func regenerateHarnessFiles(projectRoot, leoDir string, cfg *config.Config) error {
	registry := harness.NewRegistry(projectRoot)

	runtimeCfg := buildRuntimeConfig(cfg)
	runtimeConstraints := buildRuntimeConstraints()
	runtimeSkills := buildRuntimeSkills()
	runtimeIdentity := buildRuntimeIdentity()

	for _, rt := range cfg.EnabledHarnesses() {
		adapter, ok := registry.Get(rt)
		if !ok {
			continue
		}
		if err := adapter.GenerateContextFile(runtimeCfg, runtimeConstraints, runtimeSkills, runtimeIdentity); err != nil {
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
		if e, ok := adapter.(harness.ExtensionInstaller); ok {
			if err := e.RegisterExtension(); err != nil {
				return fmt.Errorf("registering %s extension: %w", rt, err)
			}
		}
	}

	return nil
}

// scrubDeadConfigFields reads config.yaml from leoDir, removes the retired
// "tiers" key from every harness block and the "autonomy" key from the user
// block, and returns the cleaned bytes plus a changed flag. It does nothing
// when the keys are already absent.
//
// The scrub operates on the raw YAML node tree so that comments and
// formatting are preserved as much as possible.
func scrubDeadConfigFields(leoDir string) (scrubbed []byte, changed bool, err error) {
	configPath := filepath.Join(leoDir, "config.yaml")
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

// migrateLeoToMom copies a .leo/ directory to .mom/ at the same level.
// The .leo/ directory is preserved (not deleted) — users can remove it manually
// or it will be removed. Returns actions describing what was done.
// If .mom/ already exists, the migration is skipped.
func migrateLeoToMom(leoDir string) ([]upgradeAction, error) {
	parent := filepath.Dir(leoDir)
	momDir := filepath.Join(parent, ".mom")

	if _, err := os.Stat(momDir); err == nil {
		return nil, nil
	}

	var actions []upgradeAction

	if err := copyDirRecursive(leoDir, momDir); err != nil {
		return nil, fmt.Errorf("copying .leo/ to .mom/: %w", err)
	}

	actions = append(actions, upgradeAction{"✔", fmt.Sprintf("migrated %s → %s", leoDir, momDir)})
	actions = append(actions, upgradeAction{"⚠", ".leo/ preserved — remove it manually after verifying .mom/ works"})

	return actions, nil
}

// copyDirRecursive recursively copies src directory to dst.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, info.Mode())
	})
}
