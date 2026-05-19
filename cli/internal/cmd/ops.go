package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/cli/internal/adapters/storage"
	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/daemon"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/memory"
	"github.com/momhq/mom/cli/internal/project"
	"github.com/momhq/mom/cli/internal/ux"
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
	path, err := centralvault.Path()
	if err != nil {
		return err
	}
	lib, closeFn, err := centralvault.OpenLibrarian()
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

// validateAllDocs reads and validates every .json file in dir.
// label is used for log messages (e.g. "doc", "constraint", "skill").
// Returns (errorCount, set of valid doc IDs on disk).
func validateAllDocs(p *ux.Printer, dir string, label string) (int, map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Dir unreadable or missing — already reported.
		return 0, nil
	}

	diskDocIDs := make(map[string]bool)
	errors := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, e.Name())
		doc, loadErr := memory.LoadDoc(path)
		if loadErr != nil {
			p.Failf("%s %s: %v", label, e.Name(), loadErr)
			errors++
			continue
		}

		// Always register the doc ID for index consistency checks,
		// even if validation fails — the file exists on disk.
		diskDocIDs[doc.ID] = true

		if valErr := doc.Validate(); valErr != nil {
			p.Failf("%s %s: %v", label, e.Name(), valErr)
			errors++
			continue
		}
	}

	if errors == 0 && len(diskDocIDs) > 0 {
		p.Checkf("%ss: all %d valid", label, len(diskDocIDs))
	} else if errors > 0 {
		p.Failf("%ss: %d failed validation", label, errors)
	}

	return errors, diskDocIDs
}

// checkIndexConsistency compares the index to the docs actually on disk.
// Returns true if there are hard failures.
func checkIndexConsistency(p *ux.Printer, momDir string, diskDocIDs map[string]bool) bool {
	adapter := storage.NewIndexedAdapter(momDir)
	defer adapter.Close()
	idx, err := adapter.List()
	if err != nil {
		p.Warnf("index: could not read — %v", err)
		return false
	}

	// Collect all IDs referenced in the index.
	indexIDs := make(map[string]bool)
	for _, ids := range idx.ByScope {
		for _, id := range ids {
			indexIDs[id] = true
		}
	}

	// Orphan index entries: referenced in index but file is gone.
	orphanEntries := 0
	for id := range indexIDs {
		if diskDocIDs != nil && !diskDocIDs[id] {
			p.Warnf("index: orphan entry — %q not on disk", id)
			orphanEntries++
		}
	}

	// Orphan files: on disk but not in index.
	orphanFiles := 0
	for id := range diskDocIDs {
		if !indexIDs[id] {
			p.Warnf("index: orphan file — %q not in index", id)
			orphanFiles++
		}
	}

	if orphanEntries > 0 || orphanFiles > 0 {
		p.Failf("index consistency: %d orphan entries, %d orphan files", orphanEntries, orphanFiles)
		return true
	}

	p.Check("index consistency: ok")
	return false
}
