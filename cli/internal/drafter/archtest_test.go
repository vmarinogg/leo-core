package drafter_test

import (
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/archtest"
)

// TestDrafterImportGraph locks the boundary "Drafter is a Herald
// subscriber, period." The package is allowed to import:
//
//   - stdlib (no further restriction)
//   - jdkato/prose, toadharvard/stopwords, golang.org/x/text — the
//     algorithm dependencies for boundary detection / RAKE / POS.
//   - momhq/mom/cli/internal/herald — the bus it subscribes to.
//   - momhq/mom/cli/internal/librarian — the persistence boundary.
//
// Any other internal package import (storage, scope, watcher,
// logbook, mcp, recorder, …) is a regression. Drafter is not allowed
// to peek at the project file system, the runtime adapter shapes, or
// the MCP surface. If a future feature needs that signal, route it
// through Herald.
func TestDrafterImportGraph(t *testing.T) {
	imports := archtest.DirectImports(t, ".")

	allowedInternal := map[string]bool{
		"github.com/momhq/mom/cli/internal/herald":    true,
		"github.com/momhq/mom/cli/internal/librarian": true,
	}

	for _, imp := range imports {
		if !strings.HasPrefix(imp, "github.com/momhq/mom/cli/internal/") {
			continue // stdlib + 3rd-party; out of scope for this rule.
		}
		if !allowedInternal[imp] {
			t.Errorf("drafter must not import %q — route through Herald instead.\n"+
				"Allowed internal imports: herald, librarian.", imp)
		}
	}
}
