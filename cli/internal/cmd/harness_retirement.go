// Package cmd — harness retirement policy.
//
// Retiring a harness is a structural decision (see #342 for the
// Windsurf example): we have decided that MOM cannot reliably ingest
// data from a specific harness, so we refuse to enable it on new
// installs and prune it from legacy configs. The policy is intentionally
// centralised here so adding or removing entries is a one-place edit.
package cmd

import (
	"fmt"

	"github.com/momhq/mom/cli/internal/config"
)

// retiredHarnesses maps a retired harness name to a one-line rationale
// surfaced to users who try to enable it. Each entry should reference
// the issue that recorded the retirement decision so the why-now stays
// auditable.
var retiredHarnesses = map[string]string{
	"windsurf": "no stable transcript export from Codeium; chat data is in encrypted .pb files and the hook payload's cwd is unreliable for project scoping",
}

// rejectRetiredHarnesses returns an error if any requested harness has
// been retired, with the documented rationale. Used by `mom init` to
// fail fast before any state is written.
func rejectRetiredHarnesses(requested []string) error {
	for _, h := range requested {
		if reason, retired := retiredHarnesses[h]; retired {
			return fmt.Errorf("harness %q is retired — %s. See issue #342", h, reason)
		}
	}
	return nil
}

// pruneRetiredHarnesses removes any retired harness from cfg.Harnesses
// and emits a one-line warning per pruned entry. The in-memory mutation
// must be persisted by the caller — `mom upgrade` does this in its
// Phase 1 config.Save. Pruning here is safe for legacy installs: a
// declaration of a retired harness becomes a no-op rather than a crash.
func pruneRetiredHarnesses(cfg *config.Config, addAction func(string, string)) {
	for name, reason := range retiredHarnesses {
		if hc, present := cfg.Harnesses[name]; present && hc.Enabled {
			delete(cfg.Harnesses, name)
			addAction("⚠", fmt.Sprintf("harness %s retired — pruned from config; %s (see issue #342)", name, reason))
		}
	}
}
