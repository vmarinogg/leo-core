package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	storage "github.com/momhq/mom/storage/legacy"
)

// setupTestMemory creates a .mom/ with a JSONAdapter and returns the temp dir.
func setupTestMemory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	os.MkdirAll(filepath.Join(momDir, "memory"), 0755)

	return dir
}

func writeTestDoc(t *testing.T, dir string, doc *storage.Doc) {
	t.Helper()
	adapter := storage.NewJSONAdapter(filepath.Join(dir, ".mom"))
	if err := adapter.Write(doc); err != nil {
		t.Fatalf("writing test doc: %v", err)
	}
}

func sampleDoc(id string) *storage.Doc {
	return &storage.Doc{
		ID: id, Scope: "project",
		Tags: []string{"test"}, Created: time.Now().UTC(), CreatedBy: "test",
		Content: map[string]any{"fact": "sample fact"},
	}
}

func TestMemorySampleDocWrites(t *testing.T) {
	dir := setupTestMemory(t)
	writeTestDoc(t, dir, sampleDoc("valid-doc"))

	adapter := storage.NewJSONAdapter(filepath.Join(dir, ".mom"))
	got, err := adapter.Read("valid-doc")
	if err != nil {
		t.Fatalf("reading sample doc: %v", err)
	}
	if got.ID != "valid-doc" {
		t.Fatalf("ID = %q, want valid-doc", got.ID)
	}
}
