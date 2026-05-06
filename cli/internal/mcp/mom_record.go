package mcp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
)

// MemoryRecordEventType is re-exported from herald for backwards
// compatibility. The canonical definition lives in herald —
// herald.MemoryRecord — and both Drafter (subscriber) and this MCP
// handler (publisher) use it. Producers other than mom_record SHOULD
// NOT publish this type; it represents the "user intentionally saved"
// path that bypasses content-shaped filters. Watcher-driven captures
// use herald.TurnObserved instead.
var MemoryRecordEventType = herald.MemoryRecord

// MemoryRecordPayload is the canonical payload shape for memory.record
// events. Drafter reads these fields verbatim; nothing else inspects
// the payload, so the contract is defined here.
//
// Provenance is filled in by the handler:
//   - ProvenanceTriggerEvent = "record"
//   - ProvenanceSourceType   = "manual-draft"
//   - ProvenanceActor        = ActorAgent (claude-code, codex, …) or
//     "mcp" when the caller did not announce.
//
// Tags are normalised via librarian.NormalizeTagName before publish;
// every entry in Tags is the canonical form. If any input tag
// normalised to empty the handler rejects the WHOLE request before
// publishing — mirroring the lesson that prevented the orphan-row bug
// in the previous attempt.
type MemoryRecordPayload struct {
	SessionID              string
	Summary                string
	Tags                   []string
	Content                map[string]any
	ProvenanceActor        string
	ProvenanceSourceType   string
	ProvenanceTriggerEvent string
}

// toolMomRecord is the MCP handler. It validates inputs, normalises
// tags, then publishes the record event on the v0.30 bus. It does NOT
// call Librarian directly — Drafter is the worker that subscribes to
// memory.record and persists.
//
// Validation rules (locked):
//   - content is required, must be an object/map (not empty).
//   - session_id is required.
//   - summary is optional.
//   - tags is optional. Each tag is normalised; if any normalises to
//     empty, the entire request is rejected with a clear error and no
//     event is published (no orphan rows downstream).
func (s *Server) toolMomRecord(args map[string]any) (toolCallResult, error) {
	content, err := requireMapArg(args, "content")
	if err != nil {
		return toolCallResult{}, err
	}
	sessionID := stringArg(args, "session_id")
	if strings.TrimSpace(sessionID) == "" {
		return toolCallResult{}, errors.New("session_id is required")
	}
	summary := stringArg(args, "summary")

	rawTags, err := optionalStringSliceArg(args, "tags")
	if err != nil {
		return toolCallResult{}, err
	}
	normalisedTags, err := normaliseTagsOrReject(rawTags)
	if err != nil {
		return toolCallResult{}, err
	}

	actor := stringArg(args, "actor")
	if strings.TrimSpace(actor) == "" {
		actor = "mcp"
	}

	payload := MemoryRecordPayload{
		SessionID:              sessionID,
		Summary:                summary,
		Tags:                   normalisedTags,
		Content:                content,
		ProvenanceActor:        actor,
		ProvenanceSourceType:   "manual-draft",
		ProvenanceTriggerEvent: "record",
	}

	s.bus.Publish(herald.Event{
		Type:      herald.MemoryRecord,
		SessionID: payload.SessionID,
		Payload:   payloadAsMap(payload),
	})

	return toolCallResult{
		Content: []toolContent{{
			Type: "text",
			Text: fmt.Sprintf("recorded: session=%s tags=%v summary=%q", sessionID, normalisedTags, summary),
		}},
	}, nil
}

// normaliseTagsOrReject applies librarian.NormalizeTagName to every
// input tag. If any tag normalises to empty (e.g., "!!!", "  "), the
// whole slice is rejected with an error — the previous attempt's
// orphan-row bug came from publishing the memory then failing later on
// a per-tag UpsertTag("").
func normaliseTagsOrReject(raw []string) ([]string, error) {
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

// requireMapArg returns the named argument as a map[string]any. Each
// failure mode gets its own message so callers can tell "I forgot to
// pass it" from "I passed something but the wrong shape" from "I
// passed an object but it had no fields."
func requireMapArg(args map[string]any, key string) (map[string]any, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return nil, fmt.Errorf("%s is required", key)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("%s cannot be empty (must contain at least one field)", key)
	}
	return m, nil
}

// optionalStringSliceArg returns the named argument as []string. Absent
// or nil arg yields nil, nil. Mixed-type arrays are rejected.
func optionalStringSliceArg(args map[string]any, key string) ([]string, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(raw))
	for i, x := range raw {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", key, i)
		}
		out = append(out, s)
	}
	return out, nil
}

// payloadAsMap converts a MemoryRecordPayload to a map for the Herald
// payload bag. SessionID is NOT included — it lives on the envelope
// (herald.Event.SessionID), not in the bag. Including it here would
// duplicate the contract and re-introduce the silent-drop class of
// bug Theme A retired.
func payloadAsMap(p MemoryRecordPayload) map[string]any {
	m := map[string]any{
		"content":                  p.Content,
		"provenance_actor":         p.ProvenanceActor,
		"provenance_source_type":   p.ProvenanceSourceType,
		"provenance_trigger_event": p.ProvenanceTriggerEvent,
	}
	if p.Summary != "" {
		m["summary"] = p.Summary
	}
	if len(p.Tags) > 0 {
		m["tags"] = p.Tags
	}
	return m
}
