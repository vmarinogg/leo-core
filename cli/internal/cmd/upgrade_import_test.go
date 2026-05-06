package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/librarian"
)

func writeLegacyMemory(t *testing.T, momDir, name, body string) {
	t.Helper()
	memDir := filepath.Join(momDir, "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, name+".json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverLegacyVaultsForImport_WalksHomeMaxDepth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, "central.db"))
	legacy := filepath.Join(home, "work", "repo", ".mom")
	writeLegacyMemory(t, legacy, "a", `{"id":"a","type":"fact","tags":["Deploy"],"created":"2026-01-01T00:00:00Z","created_by":"alice","content":{"text":"deploy flow"}}`)
	tooDeep := filepath.Join(home, "a", "b", "c", "d", "e", "repo", ".mom")
	writeLegacyMemory(t, tooDeep, "b", `{"id":"b","content":{"text":"too deep"}}`)

	plans, err := discoverLegacyVaultsForImport()
	if err != nil {
		t.Fatalf("discoverLegacyVaultsForImport: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1: %+v", len(plans), plans)
	}
	if plans[0].Path != legacy {
		t.Fatalf("plan path = %s, want %s", plans[0].Path, legacy)
	}
}

func TestExecuteCentralMemoryImport_ImportsAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_UPGRADE_SCAN_ROOT", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, "central.db"))
	legacy := filepath.Join(home, "repo", ".mom")
	legacyFile := filepath.Join(legacy, "memory", "decision.json")
	body := `{"id":"decision","type":"decision","tags":["Deploy", "AWS"],"created":"2026-01-01T00:00:00Z","created_by":"alice","summary":"Use canary deploy","content":{"text":"Use AWS canary deploy"}}`
	writeLegacyMemory(t, legacy, "decision", body)

	plans, err := discoverLegacyVaultsForImport()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	summary, err := executeCentralMemoryImport(plans)
	if err != nil {
		t.Fatalf("executeCentralMemoryImport: %v", err)
	}
	if summary.Vaults != 1 || summary.Memories != 1 || len(summary.Mappings) != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if _, err := os.Stat(legacyFile); err != nil {
		t.Fatalf("legacy file was touched/removed: %v", err)
	}
	if summary.Audit == "" || !strings.Contains(summary.Audit, "upgrade") {
		t.Fatalf("audit path missing: %+v", summary)
	}

	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("OpenLibrarian: %v", err)
	}
	defer func() { _ = closeFn() }()
	rows, err := lib.SearchMemories(librarian.SearchFilter{FTSQuery: "AWS", Limit: 10})
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(rows) != 1 || rows[0].Type != "semantic" || rows[0].PromotionState != "draft" {
		t.Fatalf("imported row = %+v", rows)
	}
	ids, err := lib.MemoriesByEntity("user", "alice")
	if err != nil || len(ids) != 1 {
		t.Fatalf("created_by entity ids=%v err=%v", ids, err)
	}

	again, err := executeCentralMemoryImport(plans)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if again.Skipped != 1 || again.Memories != 0 {
		t.Fatalf("second summary = %+v", again)
	}
}

func TestLegacyDocToImportRecord_DefaultsDraftAndUntyped(t *testing.T) {
	rec, err := legacyDocToImportRecord(legacyMemoryDoc{Raw: []byte(`{"id":"x"}`), Doc: map[string]any{"id": "x", "content": map[string]any{"text": "x"}}, Hash: "h"})
	if err != nil {
		t.Fatalf("legacyDocToImportRecord: %v", err)
	}
	if rec.Memory.Type != "untyped" || rec.Memory.SessionID != "legacy-import" || rec.Memory.ProvenanceTriggerEvent != "upgrade" {
		t.Fatalf("record mapping = %+v", rec)
	}
}
