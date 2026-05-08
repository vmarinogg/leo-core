package watcher

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// WindsurfAdapter parses Windsurf JSONL transcript lines.
// Windsurf writes one JSON object per line to ~/.windsurf/transcripts/{trajectory_id}.jsonl
// with the schema:
//
//	{ "status": "done", "type": "user_input",       "user_input":       { "user_response": "..." } }
//	{ "status": "done", "type": "planner_response", "planner_response": { "response": "..." } }
//	{ "status": "done", "type": "code_action",      "code_action":      { ... } }   ← skipped
//	{ "status": "done", "type": "command_action",   "command_action":   { ... } }   ← skipped
//
// Only user_input and planner_response are ingested; all other types are dropped.
type WindsurfAdapter struct {
	// ProjectDir is the absolute path of the project to filter by.
	// If set, DetectProject is used to match transcripts.
	ProjectDir string
}

// NewWindsurfAdapter returns a new WindsurfAdapter.
func NewWindsurfAdapter() *WindsurfAdapter {
	return &WindsurfAdapter{}
}

func (a *WindsurfAdapter) Name() string { return "windsurf" }

// BelongsToProject scans the first 100 lines of a transcript looking for
// a working directory (run_command.cwd, list_directory.path, view_file.path)
// that is inside the adapter's ProjectDir.
// Returns true if a match is found, or if ProjectDir is empty (no filtering).
func (a *WindsurfAdapter) BelongsToProject(path string) bool {
	if a.ProjectDir == "" {
		return true
	}

	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	lines := 0
	for scanner.Scan() {
		lines++
		if lines > 100 {
			break
		}

		var obj map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &obj); err != nil {
			continue
		}

		t, _ := obj["type"].(string)

		// mcp_tool — check any MCP tool result for project path.
		if t == "mcp_tool" {
			if mcp, ok := obj["mcp_tool"].(map[string]any); ok {
				if result, ok := mcp["result"].(string); ok {
					if strings.Contains(result, a.ProjectDir) {
						return true
					}
				}
			}
		}

		// run_command has explicit cwd
		if t == "run_command" {
			if rc, ok := obj["run_command"].(map[string]any); ok {
				if cwd, ok := rc["cwd"].(string); ok && strings.HasPrefix(cwd, a.ProjectDir) {
					return true
				}
			}
		}

		// list_directory has file:// path
		if t == "list_directory" {
			if ld, ok := obj["list_directory"].(map[string]any); ok {
				if p, ok := ld["path"].(string); ok {
					p = strings.TrimPrefix(p, "file://")
					if strings.HasPrefix(p, a.ProjectDir) {
						return true
					}
				}
			}
		}

		// view_file has file:// path
		if t == "view_file" {
			if vf, ok := obj["view_file"].(map[string]any); ok {
				if p, ok := vf["path"].(string); ok {
					p = strings.TrimPrefix(p, "file://")
					if strings.HasPrefix(p, a.ProjectDir) {
						return true
					}
				}
			}
		}

		// grep_search output contains file paths
		if t == "grep_search_v2" {
			if gs, ok := obj["grep_search"].(map[string]any); ok {
				if out, ok := gs["output"].(string); ok {
					if strings.Contains(out, a.ProjectDir) {
						return true
					}
				}
			}
		}
	}

	return false
}

// windsurfTranscriptLine is the minimal subset of a Windsurf JSONL line
// that the adapter needs to inspect.
type windsurfTranscriptLine struct {
	Type            string                   `json:"type"`
	Status          string                   `json:"status"`
	UserInput       *windsurfUserInput       `json:"user_input,omitempty"`
	PlannerResponse *windsurfPlannerResponse `json:"planner_response,omitempty"`
}

type windsurfUserInput struct {
	UserResponse string `json:"user_response"`
}

type windsurfPlannerResponse struct {
	Response string `json:"response"`
}

// ExtractTurn parses a Windsurf transcript line into the structured
// Turn shape consumed by Drafter and Logbook. Windsurf-specific
// tool_use / usage extraction lands in a follow-up slice.
func (a *WindsurfAdapter) ExtractTurn(line []byte, sessionID string) (Turn, bool) {
	line = trimLine(line)
	if len(line) == 0 {
		return Turn{}, false
	}

	var tl windsurfTranscriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return Turn{}, false
	}

	var role, text string
	switch tl.Type {
	case "user_input":
		if tl.UserInput == nil {
			return Turn{}, false
		}
		text = strings.TrimSpace(tl.UserInput.UserResponse)
		if text == "" {
			return Turn{}, false
		}
		role = "user"
	case "planner_response":
		if tl.PlannerResponse == nil {
			return Turn{}, false
		}
		text = strings.TrimSpace(tl.PlannerResponse.Response)
		if text == "" {
			return Turn{}, false
		}
		role = "assistant"
	default:
		// Drop: code_action, command_action, file-history-snapshot,
		// hook_progress, etc.
		return Turn{}, false
	}

	// Windsurf JSONL carries no per-line timestamp field. time.Now() is the
	// best signal we have at the per-turn grain; for catch-up reads
	// this means historical turns get a wall-clock "now" — accept as
	// a known harness gap until Windsurf publishes per-line stamps.
	return Turn{
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
		Role:      role,
		Text:      text,
		Harness:   "windsurf",
	}, true
}

// CategorizeTool returns Windsurf-specific synonyms for tool names.
func (a *WindsurfAdapter) CategorizeTool(toolName string) string {
	switch toolName {
	case "code_action":
		return "codebase_write"
	case "command_action":
		return "system"
	default:
		return CategorizeToolCall(toolName)
	}
}

var _ ToolCategorizer = (*WindsurfAdapter)(nil)
