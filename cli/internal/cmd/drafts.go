package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/project"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

var (
	draftsSince         = 24 * time.Hour
	draftsProject       string
	draftsAllProjects   bool
	draftsStrictProject bool
	draftsHarness       string
	draftsSession       string
)

var draftsCmd = &cobra.Command{
	Use:   "drafts",
	Short: "List recent draft memories",
	Long: `List recent draft memories for curation by /mom-wrap-up.

By default drafts are scoped to the project that owns the current
working directory (see ADR 0016 — .mom-project.yaml). Use --all-projects
to disable scoping, --project to override, or --harness / --session to
narrow further.`,
	Args: cobra.NoArgs,
	RunE: runDrafts,
}

func init() {
	draftsCmd.Flags().DurationVar(&draftsSince, "since", 24*time.Hour, "Only show drafts newer than this duration")
	draftsCmd.Flags().StringVar(&draftsProject, "project", "", "Restrict to the named project_id (defaults to cwd-resolved project)")
	draftsCmd.Flags().BoolVar(&draftsAllProjects, "all-projects", false, "Show drafts across all projects (disables cwd scoping)")
	draftsCmd.Flags().BoolVar(&draftsStrictProject, "strict-project", false, "Exclude drafts with no project_id (legacy / unbound captures)")
	draftsCmd.Flags().StringVar(&draftsHarness, "harness", "", "Restrict to the named harness (claude-code, codex, pi)")
	draftsCmd.Flags().StringVar(&draftsSession, "session", "", "Restrict to the named session id")
}

func resetDraftsFlags() {
	draftsSince = 24 * time.Hour
	draftsProject = ""
	draftsAllProjects = false
	draftsStrictProject = false
	draftsHarness = ""
	draftsSession = ""
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

	p := ux.NewPrinter(cmd.OutOrStdout())
	scopedProjectId := resolveDraftsScope(p)

	drafts, err := lib.RecentDrafts(librarian.RecentDraftsFilter{
		Since:           draftsSince,
		Limit:           50,
		ProjectId:       scopedProjectId,
		StrictProject:   draftsStrictProject,
		SessionID:       draftsSession,
		ProvenanceActor: draftsHarness,
	})
	if err != nil {
		return fmt.Errorf("loading drafts: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "◆ drafts — last %s — %d results\n\n", draftsSince, len(drafts))
	if len(drafts) == 0 {
		return nil
	}
	fmt.Fprintln(out, "ID                                    Created               Harness         Project         Summary / excerpt")
	fmt.Fprintln(out, "────────────────────────────────────  ────────────────────  ──────────────  ──────────────  ─────────────────────────────────────────")
	for _, d := range drafts {
		gist := strings.TrimSpace(d.Summary)
		if gist == "" {
			gist = memoryTextExcerpt(d.Content, 200)
		}
		harness := truncate(orDash(d.ProvenanceActor), 14)
		proj := truncate(orDash(d.ProjectId), 14)
		fmt.Fprintf(out, "%-36s  %-20s  %-14s  %-14s  %s\n",
			d.ID, d.CreatedAt.Format("2006-01-02 15:04:05"), harness, proj, gist)
	}
	return nil
}

// resolveDraftsScope decides the project_id filter for `mom drafts`,
// mirroring `mom recall`'s policy via the shared project.ScopeForCwd.
func resolveDraftsScope(p *ux.Printer) string {
	id, hint := project.ScopeForCwd(draftsAllProjects, draftsProject)
	if hint != "" {
		p.Muted(hint)
	}
	return id
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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
