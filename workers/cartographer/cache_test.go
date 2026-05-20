package cartographer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCache_GetSet(t *testing.T) {
	c := NewCache("") // in-memory only

	// Get on empty cache.
	if _, ok := c.Get("file.go"); ok {
		t.Error("expected miss on empty cache")
	}

	entry := CacheEntry{SHA256: "abc123", LastScannedAt: time.Now().UTC().Format(time.RFC3339)}
	c.Set("file.go", entry)

	got, ok := c.Get("file.go")
	if !ok {
		t.Fatal("expected cache hit after Set")
	}
	if got.SHA256 != "abc123" {
		t.Errorf("SHA256 = %q, want abc123", got.SHA256)
	}
}

func TestCache_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")

	c := NewCache(momDir)
	c.Set("src/main.go", CacheEntry{SHA256: "deadbeef", LastScannedAt: "2026-01-01T00:00:00Z", DraftCount: 5})

	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify manifest file exists.
	manifestPath := filepath.Join(momDir, "cache", "bootstrap", "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest.json not found: %v", err)
	}

	// Load a new cache from the same dir.
	c2 := NewCache(momDir)
	got, ok := c2.Get("src/main.go")
	if !ok {
		t.Fatal("expected cache hit after reload")
	}
	if got.SHA256 != "deadbeef" {
		t.Errorf("SHA256 = %q, want deadbeef", got.SHA256)
	}
	if got.DraftCount != 5 {
		t.Errorf("DraftCount = %d, want 5", got.DraftCount)
	}
}

func TestCache_Reset(t *testing.T) {
	c := NewCache("")
	c.Set("a.go", CacheEntry{SHA256: "111"})
	c.Set("b.go", CacheEntry{SHA256: "222"})

	c.Reset()

	if _, ok := c.Get("a.go"); ok {
		t.Error("expected miss after Reset")
	}
}

func TestCache_EmptyMomDir(t *testing.T) {
	// Save on an empty-string momDir should be a no-op.
	c := NewCache("")
	if err := c.Save(); err != nil {
		t.Errorf("Save on empty momDir should not error, got %v", err)
	}
}
