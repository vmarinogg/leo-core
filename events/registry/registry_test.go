package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/events/registry"
)

func writeSchema(t *testing.T, dir, family, filename, body string) {
	t.Helper()
	famDir := filepath.Join(dir, family)
	if err := os.MkdirAll(famDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(famDir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// validSchemaJSON returns a well-formed schema document for name.
func validSchemaJSON(name string) string {
	return `{
		"name": "` + name + `",
		"description": "test schema",
		"fields": {
			"session_id": {"type": "string", "required": true},
			"text":       {"type": "string", "required": true},
			"actor":      {"type": "string", "required": true, "values": ["user","assistant","tool"]}
		}
	}`
}

func TestLoad_EmptyDirReturnsEmptyRegistry(t *testing.T) {
	r, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := r.Names(); len(got) != 0 {
		t.Fatalf("Names() = %v, want []", got)
	}
}

func TestLoad_MissingDirReturnsEmptyRegistry(t *testing.T) {
	r, err := registry.Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := r.Names(); len(got) != 0 {
		t.Fatalf("Names() = %v, want []", got)
	}
}

func TestLoad_AcceptsValidSchema(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	r, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !r.Has("capture.turn.observed") {
		t.Fatalf("expected schema registered, got %v", r.Names())
	}
}

func TestLoad_RejectsBadFamily(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "bootstrap", "session.started.json", validSchemaJSON("bootstrap.session.started"))
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected error for parked bootstrap family")
	}
	if !strings.Contains(err.Error(), "unknown family") {
		t.Fatalf("error = %v, want substring \"unknown family\"", err)
	}
}

func TestLoad_RejectsBadFilenamePattern(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "Turn.Observed.json", validSchemaJSON("capture.Turn.Observed"))
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected error for non-snake filename")
	}
	if !strings.Contains(err.Error(), "family.subject.verb") {
		t.Fatalf("error = %v, want regex mismatch error", err)
	}
}

func TestLoad_RejectsNameFilenameMismatch(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.memory.recorded"))
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected error when schema.name disagrees with filename")
	}
	if !strings.Contains(err.Error(), "does not match filename") {
		t.Fatalf("error = %v, want filename-mismatch error", err)
	}
}

func TestLoad_RejectsUnknownFieldType(t *testing.T) {
	dir := t.TempDir()
	body := `{"name":"capture.turn.observed","description":"x","fields":{"foo":{"type":"timestamp","required":true}}}`
	writeSchema(t, dir, "capture", "turn.observed.json", body)
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected error for unknown field type")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Fatalf("error = %v, want unknown-type error", err)
	}
}

func TestLoad_RejectsEnumOnNonString(t *testing.T) {
	dir := t.TempDir()
	body := `{"name":"capture.turn.observed","description":"x","fields":{"foo":{"type":"number","required":true,"values":["a","b"]}}}`
	writeSchema(t, dir, "capture", "turn.observed.json", body)
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected error for enum on non-string field")
	}
	if !strings.Contains(err.Error(), "enums only valid") {
		t.Fatalf("error = %v, want enum-on-non-string error", err)
	}
}

func TestLoad_RejectsStrayRootFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stray.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected error for stray file at schemas root")
	}
	if !strings.Contains(err.Error(), "unexpected non-dir") {
		t.Fatalf("error = %v, want stray-file error", err)
	}
}

func TestLoad_DetectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	// Force a duplicate by also writing a same-named file in a different
	// case-only difference is not possible on case-insensitive fs; instead
	// build two distinct family dirs that produce the same composed name.
	// The cheap check: use Load twice on a dir that has a duplicate file
	// path doesn't apply (filesystems dedupe). The duplicate detection in
	// Load fires when two different fs paths produce the same fullName —
	// e.g. via symlinks. Skip on platforms without symlink support.
	link := filepath.Join(dir, "capture", "turn.observed.dup.json")
	if err := os.Symlink(filepath.Join(dir, "capture", "turn.observed.json"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := registry.Load(dir)
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestValidate_UnregisteredTypeIsPermissive(t *testing.T) {
	r, _ := registry.Load(t.TempDir())
	res := r.Validate("capture.turn.observed", map[string]any{"any": 1})
	if !res.Valid || res.Marker() {
		t.Fatalf("unregistered type should be Valid=true / Marker=false; got %+v", res)
	}
}

func TestValidate_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	r, err := registry.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res := r.Validate("capture.turn.observed", map[string]any{
		"session_id": "abc",
		"text":       "hello",
		"actor":      "user",
	})
	if !res.Valid || res.Marker() {
		t.Fatalf("happy path should be Valid=true; got %+v", res)
	}
}

func TestValidate_MissingRequired_Marks(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	r, _ := registry.Load(dir)
	res := r.Validate("capture.turn.observed", map[string]any{
		"session_id": "abc",
		// text missing, actor missing
	})
	if !res.Marker() {
		t.Fatal("missing required fields must set Marker")
	}
	if !contains(res.MissingFields, "text") || !contains(res.MissingFields, "actor") {
		t.Fatalf("MissingFields = %v, want [text actor]", res.MissingFields)
	}
}

func TestValidate_TypeMismatch_Marks(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	r, _ := registry.Load(dir)
	res := r.Validate("capture.turn.observed", map[string]any{
		"session_id": 42, // wrong type
		"text":       "hi",
		"actor":      "user",
	})
	if !res.Marker() {
		t.Fatal("type mismatch must set Marker")
	}
	if len(res.TypeMismatches) != 1 || res.TypeMismatches[0].Field != "session_id" {
		t.Fatalf("TypeMismatches = %+v, want one for session_id", res.TypeMismatches)
	}
}

func TestValidate_UnknownField_PassesThrough(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	r, _ := registry.Load(dir)
	res := r.Validate("capture.turn.observed", map[string]any{
		"session_id": "abc",
		"text":       "hi",
		"actor":      "user",
		"extra":      "tolerated",
	})
	if res.Marker() {
		t.Fatalf("unknown field alone must NOT trigger marker; got %+v", res)
	}
	if !contains(res.UnknownFields, "extra") {
		t.Fatalf("UnknownFields = %v, want [extra]", res.UnknownFields)
	}
}

func TestValidate_EnumViolation_Marks(t *testing.T) {
	dir := t.TempDir()
	writeSchema(t, dir, "capture", "turn.observed.json", validSchemaJSON("capture.turn.observed"))
	r, _ := registry.Load(dir)
	res := r.Validate("capture.turn.observed", map[string]any{
		"session_id": "abc",
		"text":       "hi",
		"actor":      "bystander", // not in [user assistant tool]
	})
	if !res.Marker() {
		t.Fatal("enum violation must set Marker")
	}
	if len(res.EnumViolations) != 1 || res.EnumViolations[0].Field != "actor" {
		t.Fatalf("EnumViolations = %+v, want one for actor", res.EnumViolations)
	}
}

func TestEventNameRegex_AcceptsV1Families(t *testing.T) {
	good := []string{
		"capture.turn.observed",
		"capture.memory.recorded",
		"lifecycle.draft.promoted",
		"interaction.tool.called",
		"operational.daemon.started",
		"operational.project.bound",
	}
	for _, g := range good {
		if !registry.EventNameRegex.MatchString(g) {
			t.Errorf("regex rejected valid name %q", g)
		}
	}
}

func TestEventNameRegex_RejectsBadNames(t *testing.T) {
	bad := []string{
		"bootstrap.session.started",   // parked family
		"capture.turn.Observed",       // uppercase
		"capture.turn",                // missing verb
		"capture.turn.observed.extra", // too many parts
		"CAPTURE.turn.observed",       // uppercase family
		"random.turn.observed",        // unknown family
	}
	for _, b := range bad {
		if registry.EventNameRegex.MatchString(b) {
			t.Errorf("regex accepted invalid name %q", b)
		}
	}
}

func contains(xs []string, n string) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}
