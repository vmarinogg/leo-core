package explicitrecord

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
)

var ErrMissingSessionID = errors.New("session_id is required; do not invent one")

var sessionEnvKeys = []string{
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_SESSION_ID",
	"CODEX_THREAD_ID",
	"CODEX_SESSION_ID",
	"WINDSURF_TRAJECTORY_ID",
}

// Request is the shared explicit-write contract for `mom record` and
// `mom_record`. The caller may provide SessionID when it is a real runtime ID;
// otherwise ResolveSessionID checks known harness environment variables.
type Request struct {
	SessionID string
	Summary   string
	Tags      []string
	Content   map[string]any
	Actor     string
}

type Result struct {
	SessionID string
	Summary   string
	Tags      []string
	Actor     string
}

func Publish(bus *herald.Bus, req Request) (Result, error) {
	if bus == nil {
		return Result{}, errors.New("event bus is required")
	}
	if len(req.Content) == 0 {
		return Result{}, errors.New("content cannot be empty (must contain at least one field)")
	}
	sessionID, err := ResolveSessionID(req.SessionID)
	if err != nil {
		return Result{}, err
	}
	tags, err := NormalizeTagsOrReject(req.Tags)
	if err != nil {
		return Result{}, err
	}
	actor := strings.TrimSpace(req.Actor)
	if actor == "" {
		actor = "mcp"
	}

	bus.Publish(herald.Event{
		Type:      herald.MemoryRecord,
		SessionID: sessionID,
		Payload: map[string]any{
			"content":                  req.Content,
			"summary":                  req.Summary,
			"tags":                     tags,
			"provenance_actor":         actor,
			"provenance_source_type":   "manual-draft",
			"provenance_trigger_event": "record",
		},
	})

	return Result{SessionID: sessionID, Summary: req.Summary, Tags: tags, Actor: actor}, nil
}

func LooksLikeRuntimeSessionID(sessionID string) bool {
	s := strings.TrimSpace(sessionID)
	if s == "" {
		return false
	}
	// UUID/UUIDv7-shaped IDs are what Claude and Pi expose. Pi watcher cursor
	// filenames may prefix the UUID with a timestamp and underscore, so validate
	// the suffix in that case.
	if idx := strings.LastIndex(s, "_"); idx >= 0 && idx+1 < len(s) {
		s = s[idx+1:]
	}
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !unicode.IsDigit(r) && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}

func ResolveSessionID(explicit string) (string, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		if !LooksLikeRuntimeSessionID(s) {
			return "", fmt.Errorf("session_id %q does not look like a runtime session ID; do not invent one", s)
		}
		return s, nil
	}
	for _, key := range sessionEnvKeys {
		if s := strings.TrimSpace(os.Getenv(key)); s != "" {
			return s, nil
		}
	}
	return "", ErrMissingSessionID
}

func NormalizeTagsOrReject(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for i, t := range raw {
		n := librarian.NormalizeTagName(t)
		if n == "" {
			return nil, fmt.Errorf("tag %d (%q) normalises to empty; reject the request rather than persist a partial memory", i, t)
		}
		out = append(out, n)
	}
	return out, nil
}
