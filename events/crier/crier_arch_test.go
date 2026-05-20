package crier_test

import (
	"testing"

	"github.com/momhq/mom/shared/archtest"
)

// TestCrier_DoesNotImportVaultDirectly is the architectural invariant
// ADR 0022 names: Crier projects via Librarian and only via Librarian.
// Importing storage/vault directly would bypass the substance-
// immutability + tag-normalisation invariants Librarian enforces.
func TestCrier_DoesNotImportVaultDirectly(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/storage/vault",
	)
}

// TestCrier_DoesNotImportIngressOrBus asserts the direction of the
// data flow: Ledger → Crier → Librarian → Vault. The bus is not in
// Crier's path; ingress packages produce events that the Editor
// canonicalizes — Crier reads from the Ledger, never from a
// transient bus event.
func TestCrier_DoesNotImportIngressOrBus(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/bus/herald",
		"github.com/momhq/mom/ingress/watcher",
		"github.com/momhq/mom/ingress/cli",
		"github.com/momhq/mom/ingress/mcp",
		"github.com/momhq/mom/ingress/record",
		"github.com/momhq/mom/ingress/harness",
	)
}

// TestCrier_DoesNotImportWorkersOrServices asserts Crier is its own
// projector — not a worker, not a service. Workers subscribe to the
// bus; services read from the Vault for query-time. Crier writes the
// Vault from the Ledger; mixing roles would create a cycle.
func TestCrier_DoesNotImportWorkersOrServices(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/workers/drafter",
		"github.com/momhq/mom/workers/logbook",
		"github.com/momhq/mom/workers/cartographer",
		"github.com/momhq/mom/workers/gardener",
		"github.com/momhq/mom/services/finder",
		"github.com/momhq/mom/services/lens",
	)
}
