package watcher

import "testing"

func TestCategorizeToolCall(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		// Memory tools — bare and MCP-prefixed.
		{"mom_recall", "mom_memory"},
		{"mom_record", "mom_memory"},
		{"mom_get", "mom_memory"},
		{"mom_landmarks", "mom_memory"},
		{"mom_status", "mom_memory"},
		{"mcp__mom__mom_recall", "mom_memory"},
		{"mcp__mom__mom_record", "mom_memory"},
		// Retired MCP tool names (#349) — no longer recognised as memory
		// tools; they fall through to the system catch-all.
		{"create_memory_draft", "system"}, // renamed to mom_record
		{"mom_record_turn", "system"},     // folded into mom_record
		{"list_landmarks", "system"},      // renamed to mom_landmarks
		{"get_memory", "system"},          // renamed to mom_get
		{"search_memories", "system"},     // pre-v0.30 name; replaced by mom_recall
		// Codebase reads.
		{"Read", "codebase_read"},
		{"Grep", "codebase_read"},
		{"Glob", "codebase_read"},
		// Codebase writes.
		{"Write", "codebase_write"},
		{"Edit", "codebase_write"},
		// Anything else falls to system.
		{"Bash", "system"},
		{"WebSearch", "system"},
		{"unknown_tool", "system"},
		{"", "system"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CategorizeToolCall(tc.name)
			if got != tc.want {
				t.Errorf("CategorizeToolCall(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestCategorizeObservedToolCall_DetectsMomCLI(t *testing.T) {
	cases := []struct {
		name     string
		input    map[string]any
		wantCat  string
		wantSafe string
	}{
		{"Bash", map[string]any{"command": "mom recall release blocker details"}, "mom_cli", "mom recall"},
		{"Bash", map[string]any{"command": "mom status"}, "mom_cli", "mom status"},
		{"Bash", map[string]any{"command": "mom curate abc --type semantic --summary secret details"}, "mom_cli", "mom curate"},
		{"Bash", map[string]any{"command": "mom upgrade --dry-run"}, "mom_cli", "mom upgrade"},
		{"Bash", map[string]any{"command": "mom --version"}, "mom_cli", "mom"},
		{"Bash", map[string]any{"command": "MOM_VAULT=/tmp/mom.db mom drafts --since 1h"}, "mom_cli", "mom drafts"},
		{"Bash", map[string]any{"command": "env MOM_VAULT=/tmp/mom.db mom lens"}, "mom_cli", "mom lens"},
		{"Bash", map[string]any{"command": "cd /tmp && mom doctor"}, "mom_cli", "mom doctor"},
		{"Bash", map[string]any{"command": "echo mom recall is not executed"}, "system", "Bash"},
		{"Read", map[string]any{"file_path": "CONTEXT.md"}, "codebase_read", "Read"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/"+tc.wantSafe, func(t *testing.T) {
			gotCat, gotSafe := CategorizeObservedToolCall(tc.name, tc.input)
			if gotCat != tc.wantCat || gotSafe != tc.wantSafe {
				t.Fatalf("CategorizeObservedToolCall() = (%q, %q), want (%q, %q)", gotCat, gotSafe, tc.wantCat, tc.wantSafe)
			}
		})
	}
}

func TestNormalizeToolName(t *testing.T) {
	cases := map[string]string{
		"Read":                 "Read",
		"mcp__mom__mom_recall": "mom_recall",
		"mcp__github__create":  "create",
		"mcp__":                "mcp__", // malformed: no second separator
		"":                     "",
	}
	for in, want := range cases {
		if got := NormalizeToolName(in); got != want {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}
