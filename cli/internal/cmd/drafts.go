package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/spf13/cobra"
)

var draftsSince = 24 * time.Hour

var draftsCmd = &cobra.Command{
	Use:   "drafts",
	Short: "List recent draft memories",
	Args:  cobra.NoArgs,
	RunE:  runDrafts,
}

func init() {
	draftsCmd.Flags().DurationVar(&draftsSince, "since", 24*time.Hour, "Only show drafts newer than this duration")
}

func runDrafts(cmd *cobra.Command, _ []string) error {
	if draftsSince <= 0 {
		return fmt.Errorf("--since must be greater than zero")
	}
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = closeFn() }()

	drafts, err := lib.RecentDrafts(draftsSince, 50)
	if err != nil {
		return fmt.Errorf("loading drafts: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "◆ drafts — last %s — %d results\n\n", draftsSince, len(drafts))
	if len(drafts) == 0 {
		return nil
	}
	fmt.Fprintln(out, "ID                                    Created                 Summary / excerpt")
	fmt.Fprintln(out, "────────────────────────────────────  ─────────────────────  ─────────────────────────────────────────")
	for _, d := range drafts {
		gist := strings.TrimSpace(d.Summary)
		if gist == "" {
			gist = memoryTextExcerpt(d.Content, 200)
		}
		fmt.Fprintf(out, "%-36s  %-21s  %s\n", d.ID, d.CreatedAt.Format("2006-01-02 15:04:05"), gist)
	}
	return nil
}

func memoryTextExcerpt(content string, limit int) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return ""
	}
	text, _ := raw["text"].(string)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
