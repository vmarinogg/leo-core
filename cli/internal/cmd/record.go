package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/drafter"
	"github.com/momhq/mom/cli/internal/explicitrecord"
	"github.com/momhq/mom/cli/internal/herald"
	"github.com/spf13/cobra"
)

var (
	recordSession string
	recordSummary string
	recordTags    []string
	recordActor   string
)

var recordCmd = &cobra.Command{
	Use:    "record",
	Short:  "Save an explicit memory from CLI input (CLI mirror of the mom_record MCP tool)",
	Hidden: true,
	Long: `Reads memory text from stdin and persists it to the central vault
($HOME/.mom/mom.db) as an explicit-write memory — bypassing Drafter's
content filters per ADR 0014. Tags are normalised before insert; if
any tag normalises to empty the request is dropped without persisting.

This command is the CLI mirror of the mom_record MCP tool. It is the
human-driven path for recording a memory from a shell pipeline:

  echo "decided to use Postgres for the canary deploy" | \
    mom record --tags decision,deploy

Hook-friendly behaviour: legacy hook configs that pipe JSON to this
command silently exit 0 — the JSON shape is detected and discarded
rather than persisted as memory text.`,
	RunE:         runRecord,
	SilenceUsage: true,
}

func init() {
	recordCmd.Flags().StringVar(&recordSession, "session", "", "Real harness session ID (optional; MOM also checks harness env vars)")
	recordCmd.Flags().StringVar(&recordSummary, "summary", "", "One-line summary")
	recordCmd.Flags().StringSliceVar(&recordTags, "tags", nil, "Tag names (comma-separated; normalised before insert)")
	recordCmd.Flags().StringVar(&recordActor, "actor", "cli", "Calling agent / human label (defaults to 'cli')")
}

func runRecord(cmd *cobra.Command, _ []string) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		// Hooks pipe whatever they have; never fail at the read step.
		return nil
	}
	text := strings.TrimSpace(string(data))

	// Hook-friendly bail-outs: empty input or JSON-shaped input (legacy hook
	// payload from old Claude/Codex configs) exit 0 without writing. Missing
	// session ID for real text is now an error: agents must not invent session IDs.
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "{") {
		fmt.Fprintln(os.Stderr, "mom record: input looks like JSON (legacy hook payload?) — skipping")
		return nil
	}

	// From here on we are on the human path: stdin is non-empty and non-JSON.
	// Failures are real errors that should propagate to the user with a non-zero
	// exit; they no longer fit the hook-friendly silent-bail contract.
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("mom record: %w", err)
	}
	defer func() { _ = closeFn() }()

	bus := herald.NewBus()
	stopDrafter := drafter.New(lib).SubscribeAll(bus)
	defer stopDrafter()

	var memoryID string
	stopCapture := bus.Subscribe(herald.OpMemoryCreated, func(e herald.Event) {
		if id, _ := e.Payload["memory_id"].(string); id != "" {
			memoryID = id
		}
	})
	defer stopCapture()

	result, err := explicitrecord.Publish(bus, explicitrecord.Request{
		SessionID: recordSession,
		Summary:   recordSummary,
		Tags:      recordTags,
		Content:   map[string]any{"text": text},
		Actor:     recordActor,
	})
	if err != nil {
		return fmt.Errorf("mom record: %w", err)
	}

	if memoryID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "recorded: id=%s session=%s tags=%v\n", memoryID, result.SessionID, result.Tags)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "recorded: session=%s tags=%v\n", result.SessionID, result.Tags)
	return nil
}
