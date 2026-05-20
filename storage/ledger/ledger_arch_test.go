package ledger_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/momhq/mom/shared/archtest"
)

// TestLedger_DoesNotImportVaultOrLibrarian asserts the Ledger stays
// Layer 1: it has no opinion about the Vault projection (Crier's job)
// and no domain model. Importing storage/vault or storage/librarian
// would compromise the layering and is forbidden by ADR 0021.
func TestLedger_DoesNotImportVaultOrLibrarian(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/storage/vault",
		"github.com/momhq/mom/storage/librarian",
		"github.com/momhq/mom/storage/canonical",
		"github.com/momhq/mom/storage/memory",
		"github.com/momhq/mom/storage/legacy",
	)
}

// TestLedger_DoesNotImportIngressEventsOrWorkers asserts dependency
// direction. The Ledger is below the bus and below the events
// pipeline. Editor (ADR 0020) writes TO the Ledger; Crier (ADR 0022)
// reads FROM it. The Ledger imports neither.
func TestLedger_DoesNotImportIngressEventsOrWorkers(t *testing.T) {
	archtest.AssertNoDirectImport(t, ".",
		"github.com/momhq/mom/events/editor",
		"github.com/momhq/mom/events/registry",
		"github.com/momhq/mom/events/crier",
		"github.com/momhq/mom/ingress/watcher",
		"github.com/momhq/mom/ingress/cli",
		"github.com/momhq/mom/ingress/mcp",
		"github.com/momhq/mom/ingress/record",
		"github.com/momhq/mom/ingress/harness",
		"github.com/momhq/mom/workers/drafter",
		"github.com/momhq/mom/workers/logbook",
		"github.com/momhq/mom/workers/cartographer",
		"github.com/momhq/mom/workers/gardener",
		"github.com/momhq/mom/services/finder",
		"github.com/momhq/mom/services/lens",
	)
}

// TestLedger_DriverUsesOnlyAppendOpenMode is the strongest architectural
// invariant ADR 0021 names: the driver opens segment files with O_APPEND
// only — no O_TRUNC, no O_WRONLY without O_APPEND.
//
// Implementation: textually scan ledger.go for forbidden open-flag
// combinations. Cheap, deterministic, and immune to runtime-only
// flag changes.
func TestLedger_DriverUsesOnlyAppendOpenMode(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	forbidden := []string{
		"os.O_TRUNC",
		"O_TRUNC ",
		"O_TRUNC|",
		"|O_TRUNC",
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(pkgDir, e.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		for _, bad := range forbidden {
			if strings.Contains(string(body), bad) {
				t.Errorf("%s contains forbidden open flag %q — ADR 0021 forbids destructive opens on Ledger segments", e.Name(), bad)
			}
		}
	}
}
