package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/cli/internal/adapters/harness"
	"github.com/momhq/mom/cli/internal/config"
	"github.com/momhq/mom/cli/internal/scope"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove all MOM files from this project",
	Long: `Removes .mom/ directory and any generated harness files (e.g. .claude/CLAUDE.md, AGENTS.md).
Optionally backs up your memory before removal using the export command.`,
	RunE: runUninstall,
}

func init() {
	uninstallCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	uninstallCmd.Flags().Bool("no-backup", false, "Skip backup prompt — delete without exporting")
	uninstallCmd.Flags().Bool("force", false, "Force removal even if .mom/ is not found")
}

func runUninstall(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}

	// Find .mom/ via scope walk (works from any subdirectory).
	var momDir string
	hasLeoDir := false
	if sc, ok := scope.NearestWritable(cwd); ok {
		momDir = sc.Path
		hasLeoDir = true
	}

	if !hasLeoDir {
		force, _ := cmd.Flags().GetBool("force")
		if !force {
			return fmt.Errorf("no .mom/ directory found from %s", cwd)
		}
		momDir = filepath.Join(cwd, ".mom")
	}

	// Project root is the parent of .mom/.
	projectRoot := filepath.Dir(momDir)

	// Resolve adapters from config — use registry for all enabled harnesses.
	registry := harness.NewRegistry(projectRoot)
	var adapters []harness.Adapter

	if hasLeoDir {
		cfg, err := config.Load(momDir)
		if err == nil {
			for _, rt := range cfg.EnabledHarnesses() {
				if a, ok := registry.Get(rt); ok {
					adapters = append(adapters, a)
				}
			}
		}
	}

	// Fallback: if no adapters resolved, try all known adapters that have files present.
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

	// Last fallback: claude.
	if len(adapters) == 0 {
		if a, ok := registry.Get("claude"); ok {
			adapters = append(adapters, a)
		}
	}

	yes, _ := cmd.Flags().GetBool("yes")
	noBackup, _ := cmd.Flags().GetBool("no-backup")

	p := ux.NewPrinter(cmd.OutOrStdout())

	// Confirm with user.
	if !yes {
		p.Diamond("uninstall")
		p.Blank()
		p.Text("This will remove all MOM files from this project:")
		if hasLeoDir {
			p.Chevron(".mom/ (config, memory, cache)")
		}
		// Deduplicate file paths across adapters.
		seen := make(map[string]bool)
		for _, adapter := range adapters {
			for _, f := range adapter.GeneratedFiles() {
				if !seen[f] {
					seen[f] = true
					p.Chevron(f)
				}
			}
		}
		p.Blank()
		fmt.Fprintf(p.W, "Proceed? %s ", p.MutedText("[y/N]:"))

		scanner := bufio.NewScanner(cmd.InOrStdin())
		answer := ""
		if scanner.Scan() {
			answer = strings.TrimSpace(scanner.Text())
		}
		if strings.ToLower(answer) != "y" {
			return fmt.Errorf("uninstall aborted")
		}
	}

	// Offer backup.
	if hasLeoDir && !noBackup {
		doBackup := false
		if yes {
			doBackup = true
		} else {
			fmt.Fprintf(p.W, "  Back up memory before removing? %s ", p.MutedText("[Y/n]:"))
			scanner := bufio.NewScanner(cmd.InOrStdin())
			answer := ""
			if scanner.Scan() {
				answer = strings.TrimSpace(scanner.Text())
			}
			doBackup = answer == "" || strings.ToLower(answer) == "y"
		}

		if doBackup {
			exportDir := filepath.Join(projectRoot, "mom-export")
			p.Muted(fmt.Sprintf("exporting to %s...", exportDir))
			exportCmd.Flags().Set("output", exportDir) //nolint:errcheck
			if err := runExport(cmd, nil); err != nil {
				return fmt.Errorf("backup failed: %w — aborting uninstall", err)
			}
			p.Checkf("backup complete")
		}
	}

	// Unregister from global watch daemon.
	if err := unregisterProject(projectRoot, momDir); err != nil {
		p.Warnf("watch daemon removal: %v", err)
	} else {
		p.Check("watch daemon removed")
	}

	// Remove .mom/.
	if hasLeoDir {
		if err := os.RemoveAll(momDir); err != nil {
			return fmt.Errorf("removing .mom/: %w", err)
		}
		p.Checkf("removed .mom/")
	}

	// Remove generated harness files via adapters (deduplicated).
	removed := make(map[string]bool)
	for _, adapter := range adapters {
		for _, relPath := range adapter.GeneratedFiles() {
			if removed[relPath] {
				continue
			}
			absPath := filepath.Join(projectRoot, relPath)
			if _, err := os.Stat(absPath); err == nil {
				os.Remove(absPath)
				p.Checkf("removed %s", relPath)
				removed[relPath] = true
			}
		}

		// Remove generated dirs if empty.
		for _, relDir := range adapter.GeneratedDirs() {
			absDir := filepath.Join(projectRoot, relDir)
			entries, err := os.ReadDir(absDir)
			if err == nil && len(entries) == 0 {
				os.Remove(absDir)
				p.Checkf("removed empty %s/", relDir)
			}
		}
	}

	p.Blank()
	p.Text("MOM has been removed from this project.")
	return nil
}
