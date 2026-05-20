package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/ingress/record"
)

// MemoryRecordEventType is re-exported from herald for backwards
// compatibility. The canonical definition lives in herald —
// herald.MemoryRecord — and both Drafter (subscriber) and this MCP
// handler (publisher) use it. Producers other than mom_record SHOULD
// NOT publish this type; it represents the "user intentionally saved"
// path that bypasses content-shaped filters. Watcher-driven captures
// use herald.TurnObserved instead.
var MemoryRecordEventType = herald.MemoryRecord

// toolMomRecord is the MCP handler. It validates inputs, normalises
// tags, then publishes the record event on the v0.30 bus. It does NOT
// call Librarian directly — Drafter is the worker that subscribes to
// memory.record and persists.
//
// Validation rules (locked):
//   - content is required, must be an object/map (not empty).
//   - session_id must be real when supplied; otherwise MOM resolves known harness
//     session env vars. If no real session is available, the record is rejected.
//   - summary is optional.
//   - tags is optional. Each tag is normalised; if any normalises to
//     empty, the entire request is rejected with a clear error and no
//     event is published (no orphan rows downstream).
func (s *Server) toolMomRecord(args map[string]any) (toolCallResult, error) {
	content, err := requireMapArg(args, "content")
	if err != nil {
		return toolCallResult{}, err
	}
	rawTags, err := optionalStringSliceArg(args, "tags")
	if err != nil {
		return toolCallResult{}, err
	}

	result, err := record.Publish(s.bus, record.Request{
		SessionID: stringArg(args, "session_id"),
		Summary:   stringArg(args, "summary"),
		Tags:      rawTags,
		Content:   content,
		Actor:     stringArg(args, "actor"),
	})
	if err != nil {
		return toolCallResult{}, err
	}

	return toolCallResult{
		Content: []toolContent{{
			Type: "text",
			Text: fmt.Sprintf("recorded: session=%s tags=%v summary=%q", result.SessionID, result.Tags, result.Summary),
		}},
	}, nil
}

// requireMapArg returns the named argument as a map[string]any. Each
// failure mode gets its own message so callers can tell "I forgot to
// pass it" from "I passed something but the wrong shape" from "I
// passed an object but it had no fields."
//
// Transport tolerance (#351): some harness tool gateways serialise
// object-typed MCP parameters to JSON strings before forwarding. When
// the value arrives as a string, it is parsed and accepted only if it
// decodes to a non-empty JSON object. Strings that decode to anything
// else (primitive, array, null) or are not valid JSON are rejected
// with a clear error — the empty-object and shape invariants below
// still apply post-decode.
func requireMapArg(args map[string]any, key string) (map[string]any, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return nil, fmt.Errorf("%s is required", key)
	}
	if s, isString := v.(string); isString {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, fmt.Errorf("%s must be an object (got string that is not valid JSON: %v)", key, err)
		}
		m, isMap := parsed.(map[string]any)
		if !isMap {
			return nil, fmt.Errorf("%s must be an object (got JSON %T)", key, parsed)
		}
		v = m
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
