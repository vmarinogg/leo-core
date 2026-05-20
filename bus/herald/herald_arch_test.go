package herald_test

import (
	"testing"

	"github.com/momhq/mom/shared/archtest"
)

// TestHerald_DoesNotImportIngress asserts the bus stays agnostic of
// every ingress surface. ADR 0020's invariant: no raw adapter type
// crosses the bus boundary. The bus knows only herald.Event; the
// Editor (events/editor) is the single producer of canonical events.
//
// If a future PR adds, say, an "expose-the-watcher-Turn-on-the-bus"
// shortcut, this test fails — and the right fix is to canonicalize
// through the Editor, not loosen the rule.
func TestHerald_DoesNotImportIngress(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/ingress/watcher",
		"github.com/momhq/mom/ingress/harness",
		"github.com/momhq/mom/ingress/cli",
		"github.com/momhq/mom/ingress/mcp",
		"github.com/momhq/mom/ingress/record",
	)
}

// TestHerald_DoesNotImportEvents asserts the bus does not depend on
// the events pipeline. Direction is bus ← editor (editor publishes
// onto the bus); reversing the arrow would create a cycle and break
// the "bus is a primitive" property.
func TestHerald_DoesNotImportEvents(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/events/editor",
		"github.com/momhq/mom/events/registry",
		"github.com/momhq/mom/events/crier",
	)
}

// TestHerald_DoesNotImportStorage asserts the bus is not a durable
// storage. Persistence is Layer 1 (Ledger) and Layer 2 (Vault);
// neither belongs in the bus.
func TestHerald_DoesNotImportStorage(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/storage/vault",
		"github.com/momhq/mom/storage/librarian",
		"github.com/momhq/mom/storage/canonical",
		"github.com/momhq/mom/storage/legacy",
		"github.com/momhq/mom/storage/memory",
	)
}

// TestHerald_DoesNotImportWorkersOrServices asserts the bus is below
// every consumer. Workers (drafter, logbook) subscribe to the bus;
// services (finder, lens) read from storage. Either importing this
// way would invert the dependency.
func TestHerald_DoesNotImportWorkersOrServices(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/workers/drafter",
		"github.com/momhq/mom/workers/logbook",
		"github.com/momhq/mom/workers/cartographer",
		"github.com/momhq/mom/workers/gardener",
		"github.com/momhq/mom/services/finder",
		"github.com/momhq/mom/services/lens",
	)
}
