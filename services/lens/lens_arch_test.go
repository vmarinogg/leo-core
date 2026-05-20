package lens_test

import (
	"testing"

	"github.com/momhq/mom/shared/archtest"
)

// TestLens_DoesNotImportLedger asserts the dashboard read surface
// stays independent of Layer 1. Lens displays op_events rows that
// landed in the Vault via Logbook (bus path) and/or Crier (Ledger
// projection); Lens itself reads only the Vault projection.
func TestLens_DoesNotImportLedger(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/storage/ledger",
	)
}

// TestLens_DoesNotImportEvents asserts Lens is on the read side of
// the data flow; the event pipeline is not in scope.
func TestLens_DoesNotImportEvents(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/events/editor",
		"github.com/momhq/mom/events/registry",
		"github.com/momhq/mom/events/crier",
	)
}
