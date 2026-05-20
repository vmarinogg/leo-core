package cli

import (
	"fmt"
	"os"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/ops/daemon"
	"github.com/momhq/mom/shared/project"
	"github.com/momhq/mom/shared/ux"
	"github.com/momhq/mom/storage/librarian"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show memory status summary",
	RunE:  runStatus,
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check .mom/ health and local setup issues",
	RunE:  runDoctor,
}

// runStatus implements `mom status`.
func runStatus(cmd *cobra.Command, args []string) error {
	path, err := canonical.Path()
	if err != nil {
		return err
	}
	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = closeFn() }()

	memories, err := lib.SearchMemories(librarian.SearchFilter{Limit: 1_000_000})
	if err != nil {
		return fmt.Errorf("loading memories: %w", err)
	}
	types := map[string]int{"episodic": 0, "semantic": 0, "procedural": 0, "untyped": 0}
	curated := 0
	draft := 0
	for _, m := range memories {
		types[m.Type]++
		switch m.PromotionState {
		case "curated":
			curated++
		case "draft":
			draft++
		}
	}
	landmarks, err := lib.Landmarks(1_000_000)
	if err != nil {
		return fmt.Errorf("loading landmarks: %w", err)
	}
	opEvents, err := lib.QueryOpEvents(librarian.OpEventFilter{Limit: 1_000_000})
	if err != nil {
		return fmt.Errorf("loading op events: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	p := ux.NewPrinter(cmd.OutOrStdout())
	p.Bold("MOM")
	p.KeyValue("cwd", cwd, 12)
	if id, found := project.IdForCwd(); found {
		p.KeyValue("project", id, 12)
	} else {
		p.KeyValue("project", "(unbound — run /mom-project to bind this directory)", 12)
	}
	p.KeyValue("vault", path, 12)
	p.KeyValue("memories", fmt.Sprintf("total %d, curated %d, draft %d", len(memories), curated, draft), 12)
	p.KeyValue("types", fmt.Sprintf("episodic %d, semantic %d, procedural %d, untyped %d", types["episodic"], types["semantic"], types["procedural"], types["untyped"]), 12)
	p.KeyValue("landmarks", fmt.Sprintf("%d", len(landmarks)), 12)
	p.KeyValue("op events", fmt.Sprintf("%d", len(opEvents)), 12)
	p.KeyValue("recording", "continuous", 12)
	p.KeyValue("watcher", cliWatcherState(), 12)
	return nil
}

func cliWatcherState() string {
	health, err := daemon.StatusGlobal()
	if err != nil || len(health.Services) == 0 {
		return "unknown"
	}
	if health.Services[0].DaemonRunning {
		return "active"
	}
	return "inactive"
}
