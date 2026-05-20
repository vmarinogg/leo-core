package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/momhq/mom/storage/canonical"
	"github.com/momhq/mom/storage/memory"
	"github.com/spf13/cobra"
)

var landmarksLimit int

// landmarksCmd is the CLI mirror of the mom_landmarks MCP tool
// (per ADR 0023's CLI parity audit). Lists landmark memories from
// the central vault sorted by centrality_score descending.
//
// JSON output is emitted to stdout for non-TTY consumers; the
// structure matches what mom_landmarks returns from MCP.
var landmarksCmd = &cobra.Command{
	Use:   "landmarks",
	Short: "List landmark memories sorted by centrality",
	Long: `Returns the landmark memories from the central vault, sorted by
centrality_score descending. Emits JSON to stdout.

CLI mirror of the mom_landmarks MCP tool (per ADR 0023). Use this
in shell pipelines and as the subprocess invocation from harness
adapters once the MCP transport is retired in v0.60+.`,
	RunE: runLandmarks,
}

func init() {
	landmarksCmd.Flags().IntVar(&landmarksLimit, "limit", 20, "Maximum number of landmarks to return")
}

func runLandmarks(_ *cobra.Command, _ []string) error {
	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("open central vault: %w", err)
	}
	defer closeFn()

	items, err := lib.Landmarks(landmarksLimit)
	if err != nil {
		return fmt.Errorf("landmarks: %w", err)
	}
	out, _ := json.Marshal(items)
	fmt.Println(string(out))
	return nil
}

// countLandmarks returns the number of docs with landmark=true in memDir.
// Kept as the private helper used by `mom status` for the legacy JSON
// memory directory count.
func countLandmarks(memDir string) int {
	entries, _ := os.ReadDir(memDir)
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		doc, err := memory.LoadDoc(filepath.Join(memDir, e.Name()))
		if err != nil {
			continue
		}
		if doc.Landmark {
			n++
		}
	}
	return n
}
