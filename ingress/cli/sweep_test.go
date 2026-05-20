package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/momhq/mom/shared/config"
)

func newSweepTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	rawDir := filepath.Join(momDir, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatal(err)
	}
	return momDir
}

func createAgedFile(t *testing.T, dir, name string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("test data\n"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-age)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

func TestSweep_DeletesOldFiles(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "old.jsonl", 40*24*time.Hour)
	createAgedFile(t, rawDir, "recent.jsonl", 5*24*time.Hour)

	cfg := config.RawMemoriesConfig{RetentionDays: 30, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", result.Deleted)
	}
	if result.BytesFreed <= 0 {
		t.Error("expected bytes freed > 0")
	}
	// old.jsonl should be gone.
	if _, err := os.Stat(filepath.Join(rawDir, "old.jsonl")); !os.IsNotExist(err) {
		t.Error("old.jsonl should have been deleted")
	}
	// recent.jsonl should still exist.
	if _, err := os.Stat(filepath.Join(rawDir, "recent.jsonl")); err != nil {
		t.Error("recent.jsonl should still exist")
	}
}

func TestSweep_NeverDeletesFilesModifiedToday(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	// Write a file and leave its mtime as now (today).
	if err := os.WriteFile(filepath.Join(rawDir, "today.jsonl"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.RawMemoriesConfig{RetentionDays: 1, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", result.Deleted)
	}
	if _, err := os.Stat(filepath.Join(rawDir, "today.jsonl")); err != nil {
		t.Error("today.jsonl should still exist")
	}
}

func TestSweep_SkipsNonJsonlFiles(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "notes.txt", 40*24*time.Hour)
	createAgedFile(t, rawDir, ".draft-cursor", 40*24*time.Hour)

	cfg := config.RawMemoriesConfig{RetentionDays: 30, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 0 {
		t.Fatalf("expected 0 deleted (non-jsonl), got %d", result.Deleted)
	}
}

func TestSweep_AllWithinRetention(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "a.jsonl", 10*24*time.Hour)
	createAgedFile(t, rawDir, "b.jsonl", 20*24*time.Hour)

	cfg := config.RawMemoriesConfig{RetentionDays: 30, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", result.Deleted)
	}
}

func TestSweep_NoRawDir(t *testing.T) {
	momDir := t.TempDir()
	cfg := config.RawMemoriesConfig{RetentionDays: 30, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 0 || result.Errors != 0 {
		t.Fatalf("expected no-op, got deleted=%d errors=%d", result.Deleted, result.Errors)
	}
}

func TestSweep_ZeroRetentionMeansNeverDelete(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "ancient.jsonl", 365*24*time.Hour)

	// retention_days: 0 should default to 30.
	cfg := config.RawMemoriesConfig{RetentionDays: 0, AutoClean: false}
	result := sweep(momDir, cfg)

	// 365 days old > 30 day default, should be deleted.
	if result.Deleted != 1 {
		t.Fatalf("expected 1 deleted (0 defaults to 30), got %d", result.Deleted)
	}
}

func TestSweep_NegativeRetentionMeansNeverDelete(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "ancient.jsonl", 365*24*time.Hour)

	// Negative should also default to 30.
	cfg := config.RawMemoriesConfig{RetentionDays: -5, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 1 {
		t.Fatalf("expected 1 deleted (negative defaults to 30), got %d", result.Deleted)
	}
}

func TestSweep_LogsToFile(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "old.jsonl", 40*24*time.Hour)

	cfg := config.RawMemoriesConfig{RetentionDays: 30, AutoClean: false}
	sweep(momDir, cfg)

	logPath := filepath.Join(momDir, "logs", "sweep.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("sweep.log should exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("sweep.log should not be empty")
	}
}

func TestSweep_MultipleOldFiles(t *testing.T) {
	momDir := newSweepTestDir(t)
	rawDir := filepath.Join(momDir, "raw")

	createAgedFile(t, rawDir, "a.jsonl", 31*24*time.Hour)
	createAgedFile(t, rawDir, "b.jsonl", 60*24*time.Hour)
	createAgedFile(t, rawDir, "c.jsonl", 90*24*time.Hour)
	createAgedFile(t, rawDir, "keep.jsonl", 10*24*time.Hour)

	cfg := config.RawMemoriesConfig{RetentionDays: 30, AutoClean: false}
	result := sweep(momDir, cfg)

	if result.Deleted != 3 {
		t.Fatalf("expected 3 deleted, got %d", result.Deleted)
	}
	if _, err := os.Stat(filepath.Join(rawDir, "keep.jsonl")); err != nil {
		t.Error("keep.jsonl should still exist")
	}
}
