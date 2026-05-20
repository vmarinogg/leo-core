package registry_test

import (
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/events/registry"
)

// activeEventTypes is the set of EventType constants production code
// actually publishes onto the bus. Adding a producer requires adding
// its constant here AND registering its schema.
//
// Unused stubs in herald/herald.go (SessionStart, SessionEnd, etc.)
// are intentionally NOT in this list — they pre-date v0.50 and no
// publisher emits them today. When a producer ships, the producer's
// PR adds the constant here + registers the schema in the same change.
var activeEventTypes = []herald.EventType{
	herald.TurnObserved,
	herald.MemoryRecord,
	herald.OpMemoryCreated,
	herald.OpMemoryRedacted,
	herald.OpMemoryDropped,
}

// TestRegistry_CoversAllActiveEventTypes asserts every active
// EventType has a registered schema, and vice versa — no schema is
// orphaned without a producer constant. Surfacing drift between code
// and schemas at PR time is the whole point of ADR 0019's level B.
func TestRegistry_CoversAllActiveEventTypes(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	schemasDir := filepath.Join(filepath.Dir(thisFile), "schemas")
	r, err := registry.Load(schemasDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	registered := make(map[string]bool)
	for _, n := range r.Names() {
		registered[n] = true
	}
	active := make(map[string]bool)
	for _, et := range activeEventTypes {
		active[string(et)] = true
	}

	var missing, orphan []string
	for name := range active {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	for name := range registered {
		if !active[name] {
			orphan = append(orphan, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(orphan)

	if len(missing) > 0 {
		t.Errorf("EventType constants without registered schemas:\n  %v", missing)
	}
	if len(orphan) > 0 {
		t.Errorf("registered schemas without producer constants (add to activeEventTypes or remove the schema):\n  %v", orphan)
	}
}

// TestRegistry_ActiveEventTypesMatchTaxonomy asserts every active
// EventType is a family.subject.verb name (ADR 0018). Producers that
// publish legacy kebab-case names are caught here.
func TestRegistry_ActiveEventTypesMatchTaxonomy(t *testing.T) {
	for _, et := range activeEventTypes {
		if !registry.EventNameRegex.MatchString(string(et)) {
			t.Errorf("herald.EventType %q does not match family.subject.verb (ADR 0018)", et)
		}
	}
}
