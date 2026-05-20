// Package cmd — project binding (ADR 0016).
//
// `mom project bind --id <slug>` writes a .mom-project.yaml at the current
// working directory, declaring the project identity that gets stamped on
// every memory captured from this directory or any subdirectory.
//
// Per ADR 0016 this CLI is the scriptable primitive; the interactive
// front door is the /mom-project skill which shells to this command.
package cli

import (
	"fmt"
	"os"

	"github.com/momhq/mom/shared/config"
	"github.com/momhq/mom/shared/project"
	"github.com/spf13/cobra"
)

var (
	projectBindId    string
	projectBindForce bool
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage MOM project bindings",
	Long: `Manage the .mom-project.yaml binding that declares this directory's
project identity (per ADR 0016). Memories captured from a bound directory
carry the declared id; recall scopes to that id by default.`,
}

var projectBindCmd = &cobra.Command{
	Use:   "bind",
	Short: "Bind the current directory to a project id",
	Long: `Write a .mom-project.yaml file at the current working directory.

The file should be checked into version control so the binding travels with
the repository across machines, clones, and forks.

Examples:
  mom project bind --id pi-agents-cli
  mom project bind --id my-service --force   # overwrite an existing binding`,
	RunE: runProjectBind,
}

func init() {
	projectBindCmd.Flags().StringVar(&projectBindId, "id", "", "Project id to declare (required)")
	projectBindCmd.Flags().BoolVar(&projectBindForce, "force", false, "Overwrite an existing binding with a different id")
	_ = projectBindCmd.MarkFlagRequired("id")
	projectCmd.AddCommand(projectBindCmd)
}

func runProjectBind(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting cwd: %w", err)
	}
	if err := project.WriteBinding(cwd, projectBindId, projectBindForce); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "bound %s to project %q\n", cwd, projectBindId)

	// Per #388: binding expresses user intent that this directory should be
	// watched. Register with the global daemon so a running daemon picks it
	// up via the registry's fsnotify path, and so subsequent watch runs find
	// it. Skipped silently when MOM is not initialized — the binding file is
	// still useful (e.g. checked into a repo before MOM is installed on a
	// new machine), and `mom init`/`mom upgrade` will register later.
	projectRoot, momDir, err := resolveMomContext(cwd)
	if err != nil {
		return nil
	}
	cfg, err := config.Load(momDir)
	if err != nil {
		return nil
	}
	if err := ensureGlobalDaemon(projectRoot, momDir, cfg.EnabledHarnesses()); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: registering with watch daemon: %v\n", err)
	}
	return nil
}
