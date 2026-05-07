package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "mom",
	Short: "MOM — Memory Oriented Machine",
	Long:  "A living knowledge infrastructure where humans and agents think, decide, and evolve together.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if warning := checkVersionCache(); warning != "" {
			fmt.Fprintln(os.Stderr, warning)
			fmt.Fprintln(os.Stderr)
		}
		refreshVersionCacheAsync()
	},
}

func Execute() error {
	// Hide cobra's auto-generated completion command.
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(curateCmd)
	rootCmd.AddCommand(mapCmd)
	rootCmd.AddCommand(bootstrapAliasCmd)
	rootCmd.AddCommand(recallCmd)
	rootCmd.AddCommand(draftsCmd)
	rootCmd.AddCommand(tourCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(recordCmd)
	rootCmd.AddCommand(diagnoseCmd)
	rootCmd.AddCommand(sweepCmd)
	rootCmd.AddCommand(reindexCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(demoCmd)
	rootCmd.AddCommand(lensCmd)
}
