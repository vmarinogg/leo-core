package editor_test

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/events/editor"
	"github.com/momhq/mom/events/registry"
)

// recordingBus is a test double for editor.Bus that captures published events.
type recordingBus struct {
	events []herald.Event
}

func (r *recordingBus) Publish(e herald.Event) { r.events = append(r.events, e) }

// staticInput is a minimal Canonicalizer for testing — declares its own
// (type, payload) so the test controls both sides of the contract.
type staticInput struct {
	eventType herald.EventType
	payload   map[string]any
}

func (s staticInput) Canonical() (herald.EventType, map[string]any) {
	return s.eventType, s.payload
}

func TestCanonicalize_StampsProvenanceWhenMissing(t *testing.T) {
	bus := &recordingBus{}
	e := editor.New(bus, nil, nil)
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "abc"},
	}, editor.Source{Adapter: "claude-code"})
	if got := ev.Payload["provenance_actor"]; got != "claude-code" {
		t.Fatalf("provenance_actor = %v, want claude-code", got)
	}
}

func TestCanonicalize_PreservesExistingProvenance(t *testing.T) {
	e := editor.New(&recordingBus{}, nil, nil)
	ev := e.Canonicalize(staticInput{
		eventType: "capture.memory.recorded",
		payload:   map[string]any{"provenance_actor": "cli", "session_id": "s"},
	}, editor.Source{Adapter: "mcp"})
	if got := ev.Payload["provenance_actor"]; got != "cli" {
		t.Fatalf("provenance_actor = %v, want cli (preserved)", got)
	}
}

func TestCanonicalize_ResolvesProjectIDFromCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mom-project.yaml"),
		[]byte("# MOM project binding\nversion: \"1\"\nid: editor-test\n"), 0o644); err != nil {
		t.Fatalf("write binding: %v", err)
	}
	e := editor.New(&recordingBus{}, nil, nil)
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "abc"},
	}, editor.Source{Adapter: "claude-code", Cwd: dir})
	if got := ev.Payload["project_id"]; got != "editor-test" {
		t.Fatalf("project_id = %v, want editor-test (resolved from .mom-project.yaml)", got)
	}
}

func TestCanonicalize_PreservesExistingProjectID(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mom-project.yaml"),
		[]byte("version: \"1\"\nid: from-disk\n"), 0o644); err != nil {
		t.Fatalf("write binding: %v", err)
	}
	e := editor.New(&recordingBus{}, nil, nil)
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"project_id": "from-payload", "session_id": "abc"},
	}, editor.Source{Adapter: "claude-code", Cwd: dir})
	if got := ev.Payload["project_id"]; got != "from-payload" {
		t.Fatalf("project_id = %v, want from-payload (payload value wins)", got)
	}
}

func TestPublish_EmitsThroughBus(t *testing.T) {
	bus := &recordingBus{}
	e := editor.New(bus, nil, nil)
	e.Publish(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "s1", "text": "hi"},
	}, editor.Source{Adapter: "codex"})
	if len(bus.events) != 1 {
		t.Fatalf("bus.events len = %d, want 1", len(bus.events))
	}
	got := bus.events[0]
	if got.Type != "capture.turn.observed" {
		t.Errorf("Type = %q, want capture.turn.observed", got.Type)
	}
	if got.SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1", got.SessionID)
	}
	if got.Payload["text"] != "hi" {
		t.Errorf("Payload[text] = %v, want hi", got.Payload["text"])
	}
}

func TestCanonicalize_NoSchemaViolation_NoMarker(t *testing.T) {
	dir := writeSchemaDir(t, "capture", "turn.observed.json", `{
		"name": "capture.turn.observed",
		"description": "x",
		"fields": {
			"session_id": {"type": "string", "required": true},
			"text":       {"type": "string", "required": true},
			"actor":      {"type": "string", "required": true, "values": ["user","assistant"]}
		}
	}`)
	reg, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e := editor.New(&recordingBus{}, reg, log.New(&bytes.Buffer{}, "", 0))
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "abc", "text": "hi", "actor": "user"},
	}, editor.Source{Adapter: "claude-code"})
	if _, has := ev.Payload["_schema_violation"]; has {
		t.Fatalf("happy path attached _schema_violation: %+v", ev.Payload["_schema_violation"])
	}
}

func TestCanonicalize_MissingRequired_AttachesMarker(t *testing.T) {
	dir := writeSchemaDir(t, "capture", "turn.observed.json", `{
		"name": "capture.turn.observed",
		"description": "x",
		"fields": {
			"session_id": {"type": "string", "required": true},
			"text":       {"type": "string", "required": true}
		}
	}`)
	reg, _ := registry.Load(dir)
	e := editor.New(&recordingBus{}, reg, log.New(&bytes.Buffer{}, "", 0))
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "abc"}, // text missing
	}, editor.Source{Adapter: "claude-code"})
	marker, has := ev.Payload["_schema_violation"]
	if !has {
		t.Fatal("expected _schema_violation marker for missing required field")
	}
	m, ok := marker.(map[string]any)
	if !ok {
		t.Fatalf("marker = %T, want map[string]any", marker)
	}
	missing, _ := m["missing_required"].([]string)
	if len(missing) != 1 || missing[0] != "text" {
		t.Fatalf("missing_required = %v, want [text]", missing)
	}
}

func TestCanonicalize_TypeMismatch_AttachesMarker(t *testing.T) {
	dir := writeSchemaDir(t, "capture", "turn.observed.json", `{
		"name": "capture.turn.observed",
		"description": "x",
		"fields": {
			"session_id": {"type": "string", "required": true},
			"text":       {"type": "string", "required": true}
		}
	}`)
	reg, _ := registry.Load(dir)
	e := editor.New(&recordingBus{}, reg, log.New(&bytes.Buffer{}, "", 0))
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": 42, "text": "hi"}, // wrong type
	}, editor.Source{Adapter: "claude-code"})
	marker, has := ev.Payload["_schema_violation"].(map[string]any)
	if !has {
		t.Fatal("expected _schema_violation map for type mismatch")
	}
	if _, ok := marker["type_mismatches"]; !ok {
		t.Fatalf("marker = %v, want type_mismatches key", marker)
	}
}

func TestCanonicalize_UnknownField_NoMarker(t *testing.T) {
	dir := writeSchemaDir(t, "capture", "turn.observed.json", `{
		"name": "capture.turn.observed",
		"description": "x",
		"fields": {
			"session_id": {"type": "string", "required": true},
			"text":       {"type": "string", "required": true}
		}
	}`)
	reg, _ := registry.Load(dir)
	e := editor.New(&recordingBus{}, reg, log.New(&bytes.Buffer{}, "", 0))
	ev := e.Canonicalize(staticInput{
		eventType: "capture.turn.observed",
		payload:   map[string]any{"session_id": "s", "text": "hi", "extra": "tolerated"},
	}, editor.Source{Adapter: "claude-code"})
	if _, has := ev.Payload["_schema_violation"]; has {
		t.Fatal("unknown field should not attach _schema_violation (level B)")
	}
	// And the unknown field survives.
	if ev.Payload["extra"] != "tolerated" {
		t.Fatalf("extra = %v, want tolerated (pass-through)", ev.Payload["extra"])
	}
}

func TestCanonicalize_UnregisteredType_NoMarker(t *testing.T) {
	reg, _ := registry.Load(t.TempDir())
	e := editor.New(&recordingBus{}, reg, log.New(&bytes.Buffer{}, "", 0))
	ev := e.Canonicalize(staticInput{
		eventType: "capture.never.registered",
		payload:   map[string]any{"session_id": "x"},
	}, editor.Source{Adapter: "claude-code"})
	if _, has := ev.Payload["_schema_violation"]; has {
		t.Fatal("unregistered type should never attach _schema_violation")
	}
}

// writeSchemaDir creates a tempdir with one schema file at family/filename.
func writeSchemaDir(t *testing.T, family, filename, body string) string {
	t.Helper()
	dir := t.TempDir()
	famDir := filepath.Join(dir, family)
	if err := os.MkdirAll(famDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(famDir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}
