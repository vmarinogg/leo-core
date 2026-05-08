package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
)

func openExportTestLib(t *testing.T, dbPath string) (*librarian.Librarian, func() error) {
	t.Helper()
	t.Setenv("MOM_VAULT", dbPath)
	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("OpenLibrarian: %v", err)
	}
	return lib, closeFn
}

func insertExportTestMemory(t *testing.T, lib *librarian.Librarian, summary string) string {
	t.Helper()
	content, _ := json.Marshal(map[string]any{"text": summary + " body"})
	id, err := lib.InsertMemoryWithTags(librarian.InsertMemory{
		Type:                   "semantic",
		Summary:                summary,
		Content:                string(content),
		SessionID:              "export-test-session",
		ProvenanceActor:        "test",
		ProvenanceSourceType:   "test",
		ProvenanceTriggerEvent: "test",
	}, []string{"export-test"})
	if err != nil {
		t.Fatalf("InsertMemoryWithTags: %v", err)
	}
	return id
}

func latestExportDir(t *testing.T, centralDir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(centralDir, "exports"))
	if err != nil {
		t.Fatalf("read exports dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no export dirs created")
	}
	return filepath.Join(centralDir, "exports", entries[len(entries)-1].Name())
}

func TestExportCmd_DumpsCentralVaultTables(t *testing.T) {
	centralDir := filepath.Join(t.TempDir(), ".mom")
	lib, closeFn := openExportTestLib(t, filepath.Join(centralDir, "mom.db"))
	insertExportTestMemory(t, lib, "Export central table memory")
	if _, err := lib.InsertOpEvent(librarian.OpEvent{EventType: "test.event", SessionID: "export-test-session", Payload: map[string]any{"ok": true}}); err != nil {
		t.Fatalf("InsertOpEvent: %v", err)
	}
	_ = closeFn()

	buf := new(bytes.Buffer)
	exportCmd.SetOut(buf)
	if err := runExport(exportCmd, nil); err != nil {
		t.Fatalf("runExport: %v", err)
	}

	exportDir := latestExportDir(t, centralDir)
	for _, name := range []string{"manifest.json", "memories.json", "tags.json", "op_events.json"} {
		if _, err := os.Stat(filepath.Join(exportDir, name)); err != nil {
			t.Fatalf("missing %s in export: %v", name, err)
		}
	}
	manifestData, err := os.ReadFile(filepath.Join(exportDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest centralExportManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if manifest.Format != centralExportFormat {
		t.Fatalf("format = %q, want %q", manifest.Format, centralExportFormat)
	}
	if manifest.Tables["memories"] != 1 || manifest.Tables["op_events"] != 1 {
		t.Fatalf("manifest counts = %+v", manifest.Tables)
	}
	if !strings.Contains(buf.String(), "exported to") {
		t.Fatalf("output missing export path: %s", buf.String())
	}
}

func TestImportCmd_CentralExportMergeSkipsExistingRows(t *testing.T) {
	srcCentral := filepath.Join(t.TempDir(), "src", ".mom")
	lib, closeFn := openExportTestLib(t, filepath.Join(srcCentral, "mom.db"))
	id := insertExportTestMemory(t, lib, "Round trip central memory")
	_ = closeFn()
	if err := runExport(exportCmd, nil); err != nil {
		t.Fatalf("source export: %v", err)
	}
	exportDir := latestExportDir(t, srcCentral)

	dstCentral := filepath.Join(t.TempDir(), "dst", ".mom")
	t.Setenv("MOM_VAULT", filepath.Join(dstCentral, "mom.db"))
	buf := new(bytes.Buffer)
	importCmd.SetOut(buf)
	if err := runImport(importCmd, []string{exportDir}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	lib, closeFn = openExportTestLib(t, filepath.Join(dstCentral, "mom.db"))
	if _, err := lib.Get(id); err != nil {
		t.Fatalf("imported memory id %s not found: %v", id, err)
	}
	_ = closeFn()

	buf.Reset()
	importCmd.SetOut(buf)
	if err := runImport(importCmd, []string{exportDir}); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if !strings.Contains(buf.String(), "skipped") {
		t.Fatalf("second import should report skipped rows, got: %s", buf.String())
	}
}

func TestImportCmd_LegacyMemoryJSONPathUsesUpgradeImportRoutine(t *testing.T) {
	legacyRoot := setupTestMemory(t)
	doc := sampleDoc("legacy-import-doc")
	doc.Content = map[string]any{"text": "legacy import body"}
	writeTestDoc(t, legacyRoot, doc)

	centralDir := filepath.Join(t.TempDir(), ".mom")
	t.Setenv("MOM_VAULT", filepath.Join(centralDir, "mom.db"))
	buf := new(bytes.Buffer)
	importCmd.SetOut(buf)
	if err := runImport(importCmd, []string{filepath.Join(legacyRoot, ".mom")}); err != nil {
		t.Fatalf("legacy import: %v", err)
	}
	if !strings.Contains(buf.String(), "1 memories imported") {
		t.Fatalf("unexpected import output: %s", buf.String())
	}

	lib, closeFn := openExportTestLib(t, filepath.Join(centralDir, "mom.db"))
	defer func() { _ = closeFn() }()
	rows, err := lib.SearchMemories(librarian.SearchFilter{SessionID: "legacy-import", Limit: 10})
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(rows) != 1 || !strings.Contains(rows[0].Memory.Content, "legacy import body") {
		t.Fatalf("legacy import rows = %+v", rows)
	}
}
