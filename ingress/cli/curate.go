package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/momhq/mom/storage/canonical"

	"github.com/momhq/mom/shared/ux"
	"github.com/momhq/mom/storage/librarian"
	"github.com/spf13/cobra"
)

var (
	curateType    string
	curateSummary string
)

var curateCmd = &cobra.Command{
	Use:   "curate <memory-id>",
	Short: "Curate a draft memory",
	Long:  `Curates a draft memory by setting its type, summary, and promotion state together.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runCurate,
}

func init() {
	curateCmd.Flags().StringVar(&curateType, "type", "", "Curated memory type: semantic, procedural, or episodic")
	curateCmd.Flags().StringVar(&curateSummary, "summary", "", "Curated one-line summary")
}

func runCurate(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	if id == "" {
		return fmt.Errorf("memory id is required")
	}
	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = closeFn() }()

	if err := lib.CurateDraft(id, curateType, curateSummary); err != nil {
		if errors.Is(err, librarian.ErrNotFound) {
			return fmt.Errorf("memory %q not found", id)
		}
		return fmt.Errorf("curating memory %q: %w", id, err)
	}
	ux.NewPrinter(cmd.OutOrStdout()).Checkf("curated %s", id)
	return nil
}
