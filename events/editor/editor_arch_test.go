package editor_test

import (
	"testing"

	"github.com/momhq/mom/shared/archtest"
)

// TestEditor_DoesNotImportVault asserts that the Editor stays on the
// canonicalization-and-bus boundary. Storage is the Crier's concern
// (ADR 0022); the Editor must not reach into the Vault directly. The
// Editor *will* gain a Ledger-append step in #366, which is allowed
// (storage/ledger is Layer 1, not Vault). Vault import is forbidden.
func TestEditor_DoesNotImportVault(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/storage/vault",
	)
}

// TestEditor_DoesNotImportWatcherAdapters asserts the Editor stays
// agnostic of any specific ingress adapter type. ADR 0020's invariant
// is "no raw adapter type crosses the bus boundary" — the Editor sits
// on the bus side of that boundary, so it must not depend on adapter
// internals. Producers hand the Editor a Canonicalizer interface;
// the concrete type stays on the ingress side.
//
// The watcher package itself contains the Turn type that implements
// Canonicalizer. Editor importing the watcher package would couple
// the gateway to a single producer.
func TestEditor_DoesNotImportIngressAdapters(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/ingress/watcher",
		"github.com/momhq/mom/ingress/harness",
		"github.com/momhq/mom/ingress/cli",
		"github.com/momhq/mom/ingress/mcp",
		"github.com/momhq/mom/ingress/record",
	)
}

// TestEditor_DoesNotImportWorkers asserts no worker is in scope. The
// Editor publishes onto the bus; subscribers (Drafter, Logbook,
// Cartographer, Gardener) react. The dependency direction is one-way.
func TestEditor_DoesNotImportWorkers(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/workers/drafter",
		"github.com/momhq/mom/workers/logbook",
		"github.com/momhq/mom/workers/cartographer",
		"github.com/momhq/mom/workers/gardener",
	)
}

// TestEditor_DoesNotImportServices asserts the read path is invisible
// to the Editor. Finder and Lens read from the Vault via Librarian;
// the Editor writes (via Crier eventually) and emits onto the bus.
// Coupling here would compromise the read/write separation.
func TestEditor_DoesNotImportServices(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/services/finder",
		"github.com/momhq/mom/services/lens",
	)
}
