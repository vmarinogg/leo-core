package cli

import (
	"os"

	"charm.land/lipgloss/v2"
	"github.com/momhq/mom/shared/ux"
	"github.com/spf13/cobra"
)

var demoCmd = &cobra.Command{
	Use:    "demo",
	Short:  "Preview MOM CLI design system patterns",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		p := ux.NewPrinter(os.Stdout)

		// Banner
		p.Banner()
		p.Blank()

		// Brand Palette
		p.Bold("Brand Palette")
		p.Blank()
		p.ColorSwatch("Ink", "#001423", lipgloss.NewStyle().Foreground(ux.Ink), 12)
		p.ColorSwatch("Paper", "#FFF5E5", lipgloss.NewStyle().Foreground(ux.Paper), 12)
		p.ColorSwatch("Signal", "#0066B1", lipgloss.NewStyle().Foreground(ux.Signal), 12)
		p.ColorSwatch("Walnut", "#3B1F0A", lipgloss.NewStyle().Foreground(ux.Walnut), 12)
		p.ColorSwatch("Archive", "#FFCC2C", lipgloss.NewStyle().Foreground(ux.Archive), 12)
		p.Blank()

		// Functional Colors
		p.Bold("Functional Colors")
		p.Blank()
		p.ColorSwatch("Success", "#608451", lipgloss.NewStyle().Foreground(ux.Success), 12)
		p.ColorSwatch("Error", "#AE4C3B", lipgloss.NewStyle().Foreground(ux.Error), 12)
		p.ColorSwatch("Warning", "#EFDD6F", lipgloss.NewStyle().Foreground(ux.Warning), 12)
		p.ColorSwatch("Muted", "#6B7B8D", lipgloss.NewStyle().Foreground(ux.Muted), 12)
		p.Blank()

		// Indicators
		p.Bold("Indicators")
		p.Blank()
		p.StepInProgress("Step in progress")
		p.StepCompleted("Step completed")
		p.Check("Check passed")
		p.Fail("Check failed")
		p.Warn("Warning")
		p.Info("Informational")
		p.Blank()

		// Spinner
		p.Bold("Spinner")
		p.Blank()
		p.StepDone("Scanning project structure")
		p.Blank()

		// Key-Value Layout
		p.Bold("Key-Value Layout (mom status)")
		p.Blank()
		w := 13
		p.KeyValue("Harnesses", "claude", w)
		p.KeyValue("Mode", "efficient", w)
		p.KeyValue("Storage", "json", w)
		p.KeyValue("Total docs", "5,452", w)
		p.KeyValue("Tags", "342 unique", w)
		p.KeyValue("Stale docs", "12", w)
		p.Blank()

		// Check List
		p.Bold("Check List (mom doctor)")
		p.Blank()
		p.Check(".mom/ directory: exists and writable")
		p.Check("config.yaml: valid (harnesses: claude)")
		p.Check("memory/: exists")
		p.Check("constraints/: exists")
		p.Warn("skills/: not found")
		p.Check("docs: all 5452 valid")
		p.Fail("index: 2 orphan entries")
		p.Check("communication mode: efficient")
		p.Warn("telemetry: disabled")
		p.Blank()
		p.Text("10 checks — 8 passed, 1 warning, 1 failed")
		p.Blank()

		// Panel
		p.Bold("Panel")
		p.Panel("Vault Status", []ux.KeyValuePair{
			{Label: "Scope", Value: "repo (~/.mom)"},
			{Label: "Memories", Value: "5,452"},
			{Label: "Landmarks", Value: "108"},
			{Label: "Record mode", Value: "continuous"},
		}, 13)
		p.Blank()

		return nil
	},
}
