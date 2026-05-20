package cli

import (
	"encoding/json"
	"fmt"

	"github.com/momhq/mom/storage/canonical"
	"github.com/spf13/cobra"
)

// getCmd is the CLI mirror of the mom_get MCP tool (per ADR 0023's
// CLI parity audit). Retrieves a single memory by ID from the central
// vault and emits its JSON to stdout.
var getCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Retrieve a single memory by ID",
	Long: `Retrieves the memory with the given UUID from the central vault
and writes its JSON representation to stdout.

CLI mirror of the mom_get MCP tool (per ADR 0023). Use this in
shell pipelines and as the subprocess invocation from harness
adapters once the MCP transport is retired in v0.60+.`,
	Args: cobra.ExactArgs(1),
	RunE: runGet,
}

func runGet(_ *cobra.Command, args []string) error {
	id := args[0]
	lib, closeFn, err := canonical.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("open central vault: %w", err)
	}
	defer closeFn()

	mem, err := lib.Get(id)
	if err != nil {
		return fmt.Errorf("get %s: %w", id, err)
	}
	out, _ := json.Marshal(mem)
	fmt.Println(string(out))
	return nil
}
