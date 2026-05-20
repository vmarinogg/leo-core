package crier_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/events/crier"
	"github.com/momhq/mom/storage/canonical"
	"github.com/momhq/mom/storage/ledger"
	"github.com/momhq/mom/storage/librarian"
	"github.com/momhq/mom/storage/vault"
)

// openLibrarian opens a fresh vault under t.TempDir() with the full
// canonical migration set (including migration 6 which crier needs).
func openLibrarian(t *testing.T) (*librarian.Librarian, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "mom.db")
	v, err := vault.Open(dbPath, canonical.Migrations())
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	lib := librarian.New(v)
	return lib, func() { _ = v.Close() }
}

func openLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger"))
	if err != nil {
		t.Fatalf("ledger.Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func ev(typ herald.EventType, session string) herald.Event {
	return herald.Event{
		Type:      typ,
		SessionID: session,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"session_id": session,
			"role":       "user",
		},
	}
}

func TestReplay_AppliesNewEvents(t *testing.T) {
	lib, closeFn := openLibrarian(t)
	defer closeFn()
	led := openLedger(t)

	// Append 3 events to the Ledger.
	for _, s := range []string{"s1", "s2", "s3"} {
		if _, err := led.Append(ev(herald.TurnObserved, s)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	c := crier.New(led, lib)
	stats, err := c.Replay()
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if stats.Applied != 3 {
		t.Errorf("Applied = %d, want 3", stats.Applied)
	}
	if stats.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", stats.Skipped)
	}
	if stats.LastOffset != 2 {
		t.Errorf("LastOffset = %d, want 2", stats.LastOffset)
	}
}

func TestReplay_IsIdempotentOnRerun(t *testing.T) {
	lib, closeFn := openLibrarian(t)
	defer closeFn()
	led := openLedger(t)
	for i := 0; i < 5; i++ {
		_, _ = led.Append(ev(herald.TurnObserved, "session"))
	}

	c := crier.New(led, lib)
	first, err := c.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if first.Applied != 5 {
		t.Fatalf("first Replay: Applied = %d, want 5", first.Applied)
	}

	// Rerun: nothing new should apply.
	second, err := c.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if second.Applied != 0 {
		t.Errorf("second Replay: Applied = %d, want 0 (idempotent)", second.Applied)
	}
}

func TestReplay_ResumesAfterCheckpoint(t *testing.T) {
	lib, closeFn := openLibrarian(t)
	defer closeFn()
	led := openLedger(t)

	for i := 0; i < 3; i++ {
		_, _ = led.Append(ev(herald.TurnObserved, "session"))
	}
	c := crier.New(led, lib)
	if _, err := c.Replay(); err != nil {
		t.Fatal(err)
	}

	// Append 2 more events after the first replay.
	for i := 0; i < 2; i++ {
		_, _ = led.Append(ev(herald.TurnObserved, "session"))
	}
	stats, err := c.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Applied != 2 {
		t.Errorf("Applied = %d, want 2 (only new events)", stats.Applied)
	}
	if stats.LastOffset != 4 {
		t.Errorf("LastOffset = %d, want 4", stats.LastOffset)
	}
}

func TestReplay_EmptyLedgerIsOk(t *testing.T) {
	lib, closeFn := openLibrarian(t)
	defer closeFn()
	led := openLedger(t)

	c := crier.New(led, lib)
	stats, err := c.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Applied != 0 || stats.Skipped != 0 {
		t.Errorf("empty replay: stats = %+v, want zero", stats)
	}
}

func TestReplay_ProjectsCorrectVaultState(t *testing.T) {
	lib, closeFn := openLibrarian(t)
	defer closeFn()
	led := openLedger(t)

	// Append one event.
	_, _ = led.Append(ev(herald.OpMemoryCreated, "session-vault-state"))

	c := crier.New(led, lib)
	if _, err := c.Replay(); err != nil {
		t.Fatal(err)
	}

	// Verify the op_event row landed via librarian.QueryOpEvents.
	rows, err := lib.QueryOpEvents(librarian.OpEventFilter{
		SessionID: "session-vault-state",
	})
	if err != nil {
		t.Fatalf("QueryOpEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].EventType != string(herald.OpMemoryCreated) {
		t.Errorf("EventType = %q, want %q", rows[0].EventType, herald.OpMemoryCreated)
	}
}

func TestReplay_FullReprojectionFromOffsetZero(t *testing.T) {
	dir := t.TempDir()
	// Use a stable ledger dir so we can reopen.
	ledDir := filepath.Join(dir, "ledger")
	led, err := ledger.Open(ledDir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_, _ = led.Append(ev(herald.TurnObserved, "repro"))
	}
	_ = led.Close()

	// Open fresh vault + reopen ledger; replay from scratch.
	v, err := vault.Open(filepath.Join(dir, "mom.db"), canonical.Migrations())
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	lib := librarian.New(v)

	led2, err := ledger.Open(ledDir)
	if err != nil {
		t.Fatal(err)
	}
	defer led2.Close()

	c := crier.New(led2, lib)
	stats, err := c.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Applied != 4 {
		t.Fatalf("full reprojection: Applied = %d, want 4", stats.Applied)
	}

	// Vault state is exactly what Crier projected.
	rows, _ := lib.QueryOpEvents(librarian.OpEventFilter{SessionID: "repro"})
	if len(rows) != 4 {
		t.Errorf("rows = %d, want 4", len(rows))
	}
}
