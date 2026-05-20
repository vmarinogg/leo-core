package registry_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/momhq/mom/events/registry"
)

// TestRegistry_OnDiskSchemasValid loads the real schemas directory and
// fails if any file violates ADR 0018 / ADR 0019 invariants:
//   - filename matches family.subject.verb pattern
//   - JSON is well-formed
//   - schema.name matches the filename
//   - every field declares an allowed type
//   - enums only on string fields
//
// This is what `make verify-registry` runs in CI.
func TestRegistry_OnDiskSchemasValid(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	schemasDir := filepath.Join(filepath.Dir(thisFile), "schemas")

	r, err := registry.Load(schemasDir)
	if err != nil {
		t.Fatalf("Load %s: %v", schemasDir, err)
	}
	t.Logf("registry: %d schema(s) loaded: %v", len(r.Names()), r.Names())
}
