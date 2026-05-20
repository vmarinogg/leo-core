package cli

import (
	"fmt"

	"github.com/momhq/mom/shared/ux"
	"github.com/spf13/cobra"
)

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "none"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the MOM CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		p := ux.NewPrinter(cmd.OutOrStdout())
		short := Commit
		if len(short) > 7 {
			short = short[:7]
		}
		p.Text(fmt.Sprintf("mom %s (%s)", p.HighlightValue(Version), p.MutedText(short)))
	},
}
