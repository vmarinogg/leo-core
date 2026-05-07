package watcher

import "strings"

// CategorizeToolCall buckets a tool name into one of the v0.30 op
// categories. Logbook persists categories (not names) on the metadata
// projection, so this is the boundary between harness-specific tool
// vocabulary and the dashboard's stable category model.
//
// Five buckets, by convention used in lens panels:
//
//	mom_memory     — memory-touching MCP tools
//	mom_cli        — mom-specific CLI invocations
//	codebase_read  — reads of repo content
//	codebase_write — writes to repo content
//	system         — everything else (Bash, Glob, harness internals…)
//
// The function lived in internal/logbook in the v1 design; it moves
// here because the watcher is the only component that sees individual
// tool names in v0.30. Logbook never categorises — it persists the
// pre-computed category from the watcher.
func CategorizeToolCall(toolName string) string {
	name := NormalizeToolName(toolName)
	switch {
	case isMemoryTool(name):
		return "mom_memory"
	case isMomCLI(name):
		return "mom_cli"
	case isCodebaseRead(name):
		return "codebase_read"
	case isCodebaseWrite(name):
		return "codebase_write"
	default:
		return "system"
	}
}

// NormalizeToolName strips runtime-specific prefixes from tool names.
// Claude Code namespaces MCP tools as "mcp__<server>__<tool>"; this
// returns the bare tool name so categorisation is harness-agnostic.
func NormalizeToolName(toolName string) string {
	if strings.HasPrefix(toolName, "mcp__") {
		if i := strings.Index(toolName[5:], "__"); i >= 0 {
			return toolName[5+i+2:]
		}
	}
	return toolName
}

func isMemoryTool(name string) bool {
	return name == "mom_recall" || name == "search_memories" || name == "get_memory" ||
		name == "create_memory_draft" || name == "list_landmarks" ||
		name == "mom_record_turn" || name == "mom_record" ||
		name == "mom_get" || name == "mom_landmarks" || name == "mom_status"
}

// CategorizeObservedToolCall returns the Lens category and privacy-safe name
// for one observed tool call. It may inspect raw shell command input while the
// event is still on the in-process bus, but it returns only coarse metadata.
func CategorizeObservedToolCall(toolName string, input map[string]any) (category, safeName string) {
	name := NormalizeToolName(toolName)
	if isShellTool(name) {
		if momName := safeMomCLIName(input); momName != "" {
			return "mom_cli", momName
		}
	}
	return CategorizeToolCall(name), name
}

func isMomCLI(name string) bool {
	return name == "mom_draft" || name == "mom_log"
}

func isShellTool(name string) bool {
	return name == "Bash" || name == "bash" || name == "Shell" || name == "shell"
}

func safeMomCLIName(input map[string]any) string {
	if input == nil {
		return ""
	}
	command, _ := input["command"].(string)
	fields := strings.Fields(strings.TrimSpace(command))
	for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") {
		fields = fields[1:]
	}
	if len(fields) == 0 || fields[0] != "mom" {
		return ""
	}
	if len(fields) == 1 || strings.HasPrefix(fields[1], "-") {
		return "mom"
	}
	sub := fields[1]
	for _, r := range sub {
		if (r < 'a' || r > 'z') && r != '-' {
			return "mom"
		}
	}
	return "mom " + sub
}

func isCodebaseRead(name string) bool {
	return name == "Read" || name == "read" || name == "Grep" || name == "grep" ||
		name == "Glob" || name == "glob" || name == "rg"
}

func isCodebaseWrite(name string) bool {
	return name == "Edit" || name == "edit" || name == "Write" || name == "write"
}
