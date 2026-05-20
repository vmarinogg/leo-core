package finder_test

import (
	"testing"

	"github.com/momhq/mom/shared/archtest"
)

// TestFinder_DoesNotImportLedger asserts the read path stays
// independent of Layer 1. Per ADR 0021 §no-reads-on-recall-path,
// the recall surface is CLI → Finder → Librarian → Vault, full stop.
// Finder must never read from storage/ledger directly.
func TestFinder_DoesNotImportLedger(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/storage/ledger",
	)
}

// TestFinder_DoesNotImportEvents asserts Finder is on the read side
// of the data flow, not the canonicalization pipeline. Events are
// produced upstream (Ingress → Editor → Ledger → Crier → Vault);
// Finder reads the resulting Vault rows.
func TestFinder_DoesNotImportEvents(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/events/editor",
		"github.com/momhq/mom/events/registry",
		"github.com/momhq/mom/events/crier",
	)
}
