package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/ingress/harness"
	"github.com/momhq/mom/ops/daemon"
	"github.com/momhq/mom/shared/config"
	"github.com/momhq/mom/shared/pathutil"
	"github.com/momhq/mom/shared/scope"
	"github.com/momhq/mom/shared/ux"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove MOM from this project or globally",
	Long: `Interactively remove MOM.

Two modes are offered:
  1) Disconnect this project — remove harness files + watch registry entry; keep central vault
  2) Full uninstall          — also remove global skills, harness context, watch daemon, central vault

No flags. The command always prompts for confirmation.`,
	RunE: runUninstall,
}

func runUninstall(cmd *cobra.Command, args []string) error {
	p := ux.NewPrinter(cmd.OutOrStdout())
	in := bufio.NewScanner(cmd.InOrStdin())

	switch promptTopMenu(p, in, cmd) {
	case "1":
		return runDisconnectProject(p, in, cmd)
	case "2":
		if !promptFullUninstallConfirmation(p, in, cmd) {
			p.Muted("uninstall cancelled")
			return nil
		}
		return runFullUninstall(p, cmd)
	default:
		p.Muted("uninstall cancelled")
		return nil
	}
}

func promptTopMenu(p *ux.Printer, in *bufio.Scanner, cmd *cobra.Command) string {
	p.Diamond("MOM uninstall")
	p.Blank()
	p.Text("What do you want to remove?")
	p.Chevron("1) Disconnect this project  (remove harness files + watch registry; keep central vault)")
	p.Chevron("2) Full uninstall           (everything: project + global skills + central vault)")
	p.Chevron("0) Cancel")
	p.Blank()
	fmt.Fprintf(cmd.OutOrStdout(), "Choose [0/1/2]: ")
	if in.Scan() {
		return strings.TrimSpace(in.Text())
	}
	return "0"
}

const fullUninstallPhrase = "delete everything"

func promptFullUninstallConfirmation(p *ux.Printer, in *bufio.Scanner, cmd *cobra.Command) bool {
	p.Blank()
	p.Bold("Full uninstall")
	p.Text("Will remove this project's MOM files, global skills, global harness context,")
	p.Text("the global watch daemon, and the central vault at ~/.mom/mom.db")
	p.Text("(ALL memory across ALL projects). This is irreversible.")
	p.Text("To keep memory, export first with `mom export`.")
	p.Blank()
	fmt.Fprintf(cmd.OutOrStdout(), "Type %q to confirm: ", fullUninstallPhrase)
	if !in.Scan() {
		return false
	}
	return strings.TrimSpace(in.Text()) == fullUninstallPhrase
}

func runFullUninstall(p *ux.Printer, cmd *cobra.Command) error {
	if err := removeGlobalWatchDaemon(p); err != nil {
		p.Warnf("removing global watch daemon: %v", err)
	}
	if err := removeGlobalHarnessContext(p); err != nil {
		p.Warnf("removing global harness context: %v", err)
	}
	if err := removeCentralVault(p); err != nil {
		p.Warnf("removing central vault: %v", err)
	}
	p.Blank()
	p.Text("MOM fully uninstalled.")
	return nil
}

func removeGlobalWatchDaemon(p *ux.Printer) error {
	// Tear down OS daemon/service files (launchd on macOS, systemd on linux).
	if err := daemon.UninstallGlobal(); err != nil {
		p.Warnf("daemon service teardown: %v", err)
	} else {
		p.Check("removed global watch daemon")
	}

	// Clear the watch registry by unregistering every entry.
	reg, err := daemon.LoadRegistry()
	if err != nil {
		return err
	}
	for projectDir := range reg {
		if err := daemon.UnregisterProject(projectDir); err != nil {
			p.Warnf("unregistering %s: %v", projectDir, err)
		}
	}
	return nil
}

func removeGlobalHarnessContext(p *ux.Printer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// Known global context files per registered harness.
	paths := []string{
		filepath.Join(home, ".claude", "CLAUDE.md"),
		filepath.Join(home, ".codex", "AGENTS.md"),
	}
	for _, path := range paths {
		if err := harness.RemoveManagedBlock(path); err != nil {
			p.Warnf("stripping MOM block from %s: %v", path, err)
			continue
		}
		if _, statErr := os.Stat(path); statErr != nil {
			p.Checkf("removed %s", path)
		}
	}
	return nil
}

func removeCentralVault(p *ux.Printer) error {
	path, err := canonical.Path()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	p.Checkf("removed central vault (%s)", path)
	return nil
}

// findProjectRoot resolves the project root for the current working
// directory. Strategy (per #303 design lock):
//  1. Look up cwd (and its ancestors) in the global watch registry.
//  2. Fall back to scope-walk for a project-local `.mom/` directory
//     (transitional users with leftover dirs from pre-v0.30 installs).
//
// Returns the project root and a flag indicating whether a project-local
// `.mom/` mirror was detected.
func findProjectRoot(cwd string) (root string, hasLocalMom bool, ok bool) {
	reg, err := daemon.LoadRegistry()
	if err == nil {
		canonicalCwd := pathutil.CanonicalDir(cwd)
		for dir := canonicalCwd; ; {
			if _, present := reg[dir]; present {
				_, statErr := os.Stat(filepath.Join(dir, ".mom"))
				return dir, statErr == nil, true
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if sc, ok := scope.NearestWritable(cwd); ok {
		return filepath.Dir(sc.Path), true, true
	}
	return "", false, false
}

func runDisconnectProject(p *ux.Printer, in *bufio.Scanner, cmd *cobra.Command) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}

	projectRoot, hasLocalMom, ok := findProjectRoot(cwd)
	if !ok {
		p.Warn("no MOM-connected project detected for this directory")
		return nil
	}

	adapters := resolveProjectAdapters(projectRoot, hasLocalMom)

	p.Blank()
	p.Textf("Project: %s", projectRoot)
	p.Text("Will remove:")
	if hasLocalMom {
		p.Chevron(".mom/ (project-local memory mirror)")
	}
	seen := make(map[string]bool)
	for _, a := range adapters {
		for _, f := range a.GeneratedFiles() {
			if !seen[f] {
				seen[f] = true
				p.Chevron(f)
			}
		}
	}
	p.Chevron("watch registry entry")
	p.Text("Central vault at ~/.mom/mom.db is NOT touched.")
	p.Blank()
	fmt.Fprintf(cmd.OutOrStdout(), "Proceed? [y/N]: ")
	answer := ""
	if in.Scan() {
		answer = strings.TrimSpace(in.Text())
	}
	if strings.ToLower(answer) != "y" {
		p.Muted("uninstall cancelled")
		return nil
	}

	// Remove project-local .mom/ if present.
	if hasLocalMom {
		momDir := filepath.Join(projectRoot, ".mom")
		if err := os.RemoveAll(momDir); err != nil {
			p.Warnf("removing .mom/: %v", err)
		} else {
			p.Check("removed .mom/")
		}
	}

	// Remove adapter-generated files.
	removed := make(map[string]bool)
	for _, a := range adapters {
		for _, rel := range a.GeneratedFiles() {
			if removed[rel] {
				continue
			}
			abs := filepath.Join(projectRoot, rel)
			if _, err := os.Stat(abs); err == nil {
				_ = os.Remove(abs)
				p.Checkf("removed %s", rel)
				removed[rel] = true
			}
		}
		for _, relDir := range a.GeneratedDirs() {
			absDir := filepath.Join(projectRoot, relDir)
			if entries, err := os.ReadDir(absDir); err == nil && len(entries) == 0 {
				_ = os.Remove(absDir)
				p.Checkf("removed empty %s/", relDir)
			}
		}
	}

	// Unregister from global watch daemon.
	if err := daemon.UnregisterProject(projectRoot); err != nil {
		p.Warnf("watch registry removal: %v", err)
	} else {
		p.Check("removed from watch registry")
	}

	p.Blank()
	p.Text("Project disconnected.")
	return nil
}

// resolveProjectAdapters returns the set of harness adapters whose files
// should be removed for this project. Strategy:
//  1. If a project-local `.mom/config.yaml` exists, use its enabled
//     harnesses (preserves legacy behavior for users mid-migration).
//  2. Otherwise, return all adapters that have at least one generated
//     file present on disk in the project root.
func resolveProjectAdapters(projectRoot string, hasLocalMom bool) []harness.Adapter {
	registry := harness.NewRegistry(projectRoot)
	var adapters []harness.Adapter

	if hasLocalMom {
		if cfg, err := config.Load(filepath.Join(projectRoot, ".mom")); err == nil {
			for _, name := range cfg.EnabledHarnesses() {
				if a, ok := registry.Get(name); ok {
					adapters = append(adapters, a)
				}
			}
		}
	}

	if len(adapters) == 0 {
		for _, a := range registry.All() {
			for _, f := range a.GeneratedFiles() {
				if _, err := os.Stat(filepath.Join(projectRoot, f)); err == nil {
					adapters = append(adapters, a)
					break
				}
			}
		}
	}

	return adapters
}
