package watcher

import (
	"encoding/json"
	"strings"
	"time"
)

// PiAdapter parses pi (https://github.com/mariozechner/pi) JSONL session files.
//
// Pi writes one JSON object per line to
//
//	~/.pi/agent/sessions/<project-slug>/<timestamp>_<sessionId>.jsonl
//
// The project slug uses the same "/" → "-" convention as Claude Code, so the
// existing projectSlug() scoping logic in watcher.go applies unchanged.
//
// Line schema (the entries we care about):
//
//	{
//	  "type":      "message",
//	  "id":        "<short-id>",
//	  "parentId":  "<short-id|null>",
//	  "timestamp": "2026-04-28T00:11:01.063Z",
//	  "message": {
//	    "role":      "user" | "assistant",
//	    "content":   string | [ {type:"text",text:"..."} | {type:"tool_use",...} | ... ],
//	    "timestamp": <unix-ms>
//	  }
//	}
//
// Other top-level "type" values exist (session, thinking_level_change,
// model_change, ...). They carry no conversational text and are dropped.
type PiAdapter struct{}

// NewPiAdapter returns a new PiAdapter.
func NewPiAdapter() *PiAdapter {
	return &PiAdapter{}
}

func (a *PiAdapter) Name() string { return "pi" }

// ProjectSlug implements ProjectScoper. Pi uses a different per-project
// directory convention than Claude/Codex: it strips the leading separator,
// replaces remaining path separators and colons with '-', and wraps the
// result with '--' on both sides.
//
// Example: /Users/foo/proj  →  --Users-foo-proj--
//
// Source-of-truth: pi-coding-agent dist/migrations.js, which builds the
// directory path as:
//
//	const safePath = `--${cwd.replace(/^[/\\]/, "").replace(/[/\\:]/g, "-")}--`;
//
// We mirror that rule exactly so the watcher's project-scoping check finds
// pi's actual session subdirectory and does not fall back to scanning all
// projects' sessions globally.
func (a *PiAdapter) ProjectSlug(projectDir string) string {
	p := projectDir
	// Strip leading path separator (Unix '/' or Windows '\').
	if len(p) > 0 && (p[0] == '/' || p[0] == '\\') {
		p = p[1:]
	}
	// Replace remaining separators and colons with '-'.
	p = strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(p)
	return "--" + p + "--"
}

// piTranscriptLine is the minimal subset of a pi session line we inspect.
type piTranscriptLine struct {
	Type      string    `json:"type"`
	Timestamp string    `json:"timestamp"`
	Message   piMessage `json:"message"`
}

type piMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []map[string]any
}

// ExtractTurn parses a Pi transcript line into the structured Turn
// shape consumed by Drafter and Logbook. Pi-specific tool_use and
// usage extraction will land in a follow-up slice.
func (a *PiAdapter) ExtractTurn(line []byte, sessionID string) (Turn, bool) {
	line = trimLine(line)
	if len(line) == 0 {
		return Turn{}, false
	}

	var tl piTranscriptLine
	if err := json.Unmarshal(line, &tl); err != nil {
		return Turn{}, false
	}

	// Drop everything except conversational message lines.
	if tl.Type != "message" {
		return Turn{}, false
	}
	if tl.Message.Role != "user" && tl.Message.Role != "assistant" {
		return Turn{}, false
	}

	text := extractPiContent(tl.Message.Content)
	if text == "" {
		return Turn{}, false
	}

	// Timestamp: prefer the line's timestamp so catch-up reads
	// preserve the turn's real time. Fall back to now only when
	// the line carries no timestamp at all. Mirrors the Claude
	// adapter so all three harnesses agree on the rule.
	ts := time.Time{}
	if tl.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, tl.Timestamp); err == nil {
			ts = t
		} else if t, err := time.Parse(time.RFC3339, tl.Timestamp); err == nil {
			ts = t
		}
	}
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	return Turn{
		SessionID: sessionID,
		Timestamp: ts,
		Role:      tl.Message.Role,
		Text:      text,
		Harness:   "pi",
	}, true
}

// extractPiContent flattens pi's content field to plain text.
//
// pi content can be:
//   - a plain string (rare, but the schema allows it)
//   - an array of blocks: {type:"text",text:"..."}, {type:"tool_use",...},
//     {type:"tool_result",...}, {type:"thinking",...}, {type:"image",...}
//
// We keep only "text" blocks. Everything else (tool I/O, thinking,
// images) is intentionally dropped to match the Claude adapter's
// behaviour and keep the central vault focused on conversational
// signal.
func extractPiContent(content any) string {
	if content == nil {
		return ""
	}

	if s, ok := content.(string); ok {
		return strings.TrimSpace(s)
	}

	items, ok := content.([]any)
	if !ok {
		return ""
	}

	var parts []string
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "text" {
			continue
		}
		if text, _ := m["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n")
}
