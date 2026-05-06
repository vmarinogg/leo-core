package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/spf13/cobra"
)

var promoteCmd = &cobra.Command{
	Use:   "promote <memory-id>",
	Short: "Promote a draft memory to curated",
	Long:  `Promotes a memory in the central vault by flipping promotion_state from draft to curated.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runPromote,
}

func runPromote(cmd *cobra.Command, args []string) error {
	id := strings.TrimSpace(args[0])
	if id == "" {
		return fmt.Errorf("memory id is required")
	}
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = closeFn() }()

	mem, err := lib.Get(id)
	if err != nil {
		if errors.Is(err, librarian.ErrNotFound) {
			return fmt.Errorf("memory %q not found", id)
		}
		return fmt.Errorf("loading memory %q: %w", id, err)
	}
	if mem.PromotionState == "curated" {
		return fmt.Errorf("memory %q is already curated", id)
	}

	state := "curated"
	if err := lib.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
		return fmt.Errorf("promoting memory %q: %w", id, err)
	}
	ux.NewPrinter(cmd.OutOrStdout()).Checkf("promoted %s to curated", id)
	return nil
}
