package crier_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/events/crier"
	"github.com/momhq/mom/events/registry"
	"github.com/momhq/mom/storage/canonical"
	"github.com/momhq/mom/storage/ledger"
	"github.com/momhq/mom/storage/librarian"
	"github.com/momhq/mom/storage/vault"
)

// minimalPayloadFor returns a payload that satisfies the required
// fields of the registry schema for name. Used by the replay harness
// to synthesize fixture events for each registered type.
func minimalPayloadFor(t *testing.T, reg *registry.Registry, name string) map[string]any {
	t.Helper()
	// The registry doesn't expose schemas directly; instead, Validate
	// returns Marker()=false when the payload is well-formed. Build a
	// reasonable shape per family then iterate Validate until clean.
	payload := map[string]any{
		"session_id":             "harness-session",
		"role":                   "user",
		"text":                   "fixture text",
		"actor":                  "user", // for enum-bound fields
		"memory_id":              "00000000-0000-4000-8000-000000000000",
		"content":                map[string]any{"text": "x"},
		"filter_category":        "secret_aws",
		"redaction_count":        float64(1),
		"drop_reason":            "noise",
		"summary":                "fixture summary",
		"provenance_actor":       "harness",
		"provenance_source_type": "manual-draft",
	}
	res := reg.Validate(name, payload)
	if res.Marker() {
		t.Fatalf("minimal payload for %s does not satisfy schema; res=%+v", name, res)
	}
	return payload
}

// TestReplay_AllRegisteredSchemas synthesizes one fixture event per
// registered schema, appends each to a fresh Ledger, replays via
// Crier, and asserts (a) all events apply, (b) the resulting vault
// state contains one op_event per event, (c) rerunning Replay is a
// no-op (idempotent), and (d) full reprojection from offset 0
// against a fresh vault produces the same row count.
//
// This is the operational definition of "self-sufficient events"
// from ADR 0018: every registered schema can be projected in
// isolation, and replay is deterministic + idempotent.
func TestReplay_AllRegisteredSchemas(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	schemasDir := filepath.Join(filepath.Dir(thisFile), "..", "registry", "schemas")
	reg, err := registry.Load(schemasDir)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	names := reg.Names()
	if len(names) == 0 {
		t.Fatal("no schemas registered — harness has nothing to project")
	}

	dir := t.TempDir()
	ledDir := filepath.Join(dir, "ledger")
	led, err := ledger.Open(ledDir)
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}

	// Append one fixture event per schema.
	for _, name := range names {
		payload := minimalPayloadFor(t, reg, name)
		ev := herald.Event{
			Type:      herald.EventType(name),
			SessionID: "harness-session",
			Timestamp: time.Now().UTC(),
			Payload:   payload,
		}
		if _, err := led.Append(ev); err != nil {
			t.Fatalf("Append %s: %v", name, err)
		}
	}
	_ = led.Close()

	// Open vault + ledger + Crier.
	v, err := vault.Open(filepath.Join(dir, "mom.db"), canonical.Migrations())
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	defer v.Close()
	lib := librarian.New(v)
	led2, _ := ledger.Open(ledDir)
	defer led2.Close()
	c := crier.New(led2, lib)

	stats, err := c.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if stats.Applied != len(names) {
		t.Errorf("Applied = %d, want %d", stats.Applied, len(names))
	}

	// Rerun: must be a no-op.
	stats2, err := c.Replay()
	if err != nil {
		t.Fatalf("Replay 2: %v", err)
	}
	if stats2.Applied != 0 {
		t.Errorf("second Replay Applied = %d, want 0 (idempotent)", stats2.Applied)
	}

	// Verify one op_event row per schema.
	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{
		SessionID: "harness-session",
	})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != len(names) {
		t.Errorf("op_events rows = %d, want %d", len(rows), len(names))
	}

	// Full reprojection: drop vault, replay from offset 0.
	v.Close()
	if err := removeFile(filepath.Join(dir, "mom.db")); err != nil {
		t.Fatalf("rm mom.db: %v", err)
	}
	v2, err := vault.Open(filepath.Join(dir, "mom.db"), canonical.Migrations())
	if err != nil {
		t.Fatalf("vault.Open (after rm): %v", err)
	}
	defer v2.Close()
	lib2 := librarian.New(v2)
	c2 := crier.New(led2, lib2)
	stats3, err := c2.Replay()
	if err != nil {
		t.Fatalf("Replay after rm: %v", err)
	}
	if stats3.Applied != len(names) {
		t.Errorf("full reprojection Applied = %d, want %d", stats3.Applied, len(names))
	}
	rows2, _ := lib2.QueryOpEvents(librarian.OpEventFilter{SessionID: "harness-session"})
	if len(rows2) != len(names) {
		t.Errorf("full reprojection rows = %d, want %d", len(rows2), len(names))
	}
}

// removeFile is a tiny helper to delete the vault file (and any
// SQLite WAL siblings) between phases of the replay test.
func removeFile(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(p)
	}
	return nil
}
