package mcp_test

import (
	"strings"
	"testing"

	"github.com/momhq/mom/ingress/mcp"
)

// expectedTools is the v0.50 MCP tool surface. ADR 0023 § parity-audit
// guarantees each of these has a CLI counterpart (see ingress/cli/
// for the matching subcommands).
var expectedTools = map[string]string{
	"mom_status":    "mom status",
	"mom_recall":    "mom recall <query>",
	"mom_get":       "mom get <id>",
	"mom_landmarks": "mom landmarks [--limit N]",
	"mom_record":    "mom record (stdin)",
}

// TestMCPToolsList_HasCLICounterparts is the parity audit: every MCP
// tool currently exposed by ingress/mcp must appear in expectedTools
// (which documents the CLI counterpart). Adding an MCP tool without
// a CLI counterpart fails this test, surfacing the gap at PR time.
func TestMCPToolsList_HasCLICounterparts(t *testing.T) {
	tools := mcp.ToolNamesForParityAudit()
	if len(tools) == 0 {
		t.Fatal("mcp.ToolNamesForParityAudit returned no tools")
	}
	for _, name := range tools {
		if _, ok := expectedTools[name]; !ok {
			t.Errorf("MCP tool %q has no documented CLI counterpart — extend expectedTools and add the matching subcommand under ingress/cli/", name)
		}
	}
	for tool := range expectedTools {
		found := false
		for _, name := range tools {
			if name == tool {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expectedTools[%q] is documented but no MCP handler ships — remove or wire it", tool)
		}
	}
}

// TestDeprecationNotice_FormatMentionsV060 is a brittle-on-purpose
// check: the boot warning text must say "v0.60" so users know when
// the transport goes away.
func TestDeprecationNotice_FormatMentionsV060(t *testing.T) {
	if !strings.Contains(mcp.DeprecationNotice(), "v0.60") {
		t.Errorf("DeprecationNotice() does not mention v0.60: %q", mcp.DeprecationNotice())
	}
	if !strings.Contains(mcp.DeprecationNotice(), "0023") {
		t.Errorf("DeprecationNotice() should point at ADR 0023: %q", mcp.DeprecationNotice())
	}
}
