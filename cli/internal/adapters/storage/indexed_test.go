package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupIndexedAdapter(t *testing.T) (*IndexedAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	os.MkdirAll(filepath.Join(momDir, "memory"), 0755) //nolint:errcheck
	a := NewIndexedAdapter(momDir)
	t.Cleanup(func() { a.Close() })
	return a, momDir
}

func indexedTestDoc(id string) *Doc {
	return &Doc{
		ID:             id,
		Scope:          "project",
		Tags:           []string{"test"},
		Created:        time.Now().UTC(),
		CreatedBy:      "test",
		PromotionState: "curated",
		Classification: "INTERNAL",
		Content:        map[string]any{"summary": "Test memory about " + id, "fact": "this is a fact"},
	}
}

func TestIndexedAdapter_WriteAndRead(t *testing.T) {
	a, _ := setupIndexedAdapter(t)
	doc := indexedTestDoc("test-write-read")

	if err := a.Write(doc); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	got, err := a.Read("test-write-read")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got.ID != "test-write-read" {
		t.Errorf("expected test-write-read, got %q", got.ID)
	}
}

func TestIndexedAdapter_WriteAndSearch(t *testing.T) {
	a, _ := setupIndexedAdapter(t)

	doc := indexedTestDoc("searchable-doc")
	doc.Content["summary"] = "Unique phrase about database indexing strategies"
	doc.Tags = []string{"database", "index", "strategy"}

	if err := a.Write(doc); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	results, err := a.Search(SearchOptions{Query: "database indexing", Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
	if results[0].ID != "searchable-doc" {
		t.Errorf("expected searchable-doc first, got %q", results[0].ID)
	}
}

func TestIndexedAdapter_DeleteRemovesFromIndex(t *testing.T) {
	a, _ := setupIndexedAdapter(t)

	doc := indexedTestDoc("to-delete-indexed")
	doc.Content["summary"] = "This will be deleted from index"
	a.Write(doc) //nolint:errcheck

	if err := a.Delete("to-delete-indexed"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	results, err := a.Search(SearchOptions{Query: "deleted from index", Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, r := range results {
		if r.ID == "to-delete-indexed" {
			t.Error("deleted doc still in search results")
		}
	}
}

func TestIndexedAdapter_BulkWrite(t *testing.T) {
	a, _ := setupIndexedAdapter(t)

	docs := []*Doc{
		indexedTestDoc("bulk-1"),
		indexedTestDoc("bulk-2"),
		indexedTestDoc("bulk-3"),
	}
	if err := a.BulkWrite(docs); err != nil {
		t.Fatalf("BulkWrite failed: %v", err)
	}

	results, err := a.Search(SearchOptions{Query: "", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestIndexedAdapter_Reindex(t *testing.T) {
	a, _ := setupIndexedAdapter(t)

	// Write docs through adapter (syncs to index).
	for _, id := range []string{"reindex-a", "reindex-b"} {
		a.Write(indexedTestDoc(id)) //nolint:errcheck
	}

	// Trigger explicit reindex.
	if err := a.Reindex(); err != nil {
		t.Fatalf("Reindex failed: %v", err)
	}

	results, err := a.Search(SearchOptions{Query: "", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results after reindex, got %d", len(results))
	}
}

func TestIndexedAdapter_ReindexRemovesStaleRowsForSameIDUnderOldScopePath(t *testing.T) {
	a, momDir := setupIndexedAdapter(t)
	doc := indexedTestDoc("stale-scope-id")
	oldScopePath := momDir + "-before-canonicalization"

	if err := a.idx.Upsert(doc, oldScopePath); err != nil {
		t.Fatalf("seed stale index row: %v", err)
	}
	if err := a.json.Write(doc); err != nil {
		t.Fatalf("write JSON source doc: %v", err)
	}

	if err := a.Reindex(); err != nil {
		t.Fatalf("Reindex failed: %v", err)
	}

	oldCount, err := a.idx.CountByScope(oldScopePath)
	if err != nil {
		t.Fatalf("CountByScope old: %v", err)
	}
	if oldCount != 0 {
		t.Fatalf("old scope count = %d, want 0", oldCount)
	}
	newCount, err := a.idx.CountByScope(momDir)
	if err != nil {
		t.Fatalf("CountByScope new: %v", err)
	}
	if newCount != 1 {
		t.Fatalf("new scope count = %d, want 1", newCount)
	}
}

func TestIndexedAdapter_ExcludeDrafts(t *testing.T) {
	a, _ := setupIndexedAdapter(t)

	curated := indexedTestDoc("curated-memory")
	curated.Content["summary"] = "Curated memory about patterns"
	curated.PromotionState = "curated"
	curated.Tags = []string{"pattern", "curated"}
	a.Write(curated) //nolint:errcheck

	draft := indexedTestDoc("draft-memory")
	draft.Content["summary"] = "Draft memory about patterns"
	draft.PromotionState = "draft"
	draft.Tags = []string{"pattern", "draft"}
	a.Write(draft) //nolint:errcheck

	results, err := a.Search(SearchOptions{Query: "pattern", ExcludeDrafts: true, Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, r := range results {
		if r.PromotionState == "draft" {
			t.Errorf("draft doc %q in results with ExcludeDrafts=true", r.ID)
		}
	}
}

func TestIndexedAdapter_FallbackSearch_NoIndex(t *testing.T) {
	// Create adapter without SQLite (simulate degraded mode).
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	os.MkdirAll(filepath.Join(momDir, "memory"), 0755) //nolint:errcheck

	a := &IndexedAdapter{
		json:      NewJSONAdapter(momDir),
		momDir:    momDir,
		scopePath: momDir,
		idx:       nil, // degraded — no SQLite
	}

	doc := indexedTestDoc("fallback-doc")
	doc.Content["summary"] = "Fallback search test memory"
	doc.Tags = []string{"fallback", "test"}
	a.json.Write(doc) //nolint:errcheck

	results, err := a.Search(SearchOptions{Query: "fallback", Limit: 5})
	if err != nil {
		t.Fatalf("Fallback search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected fallback search to find at least one result")
	}
	if results[0].ID != "fallback-doc" {
		t.Errorf("expected fallback-doc, got %q", results[0].ID)
	}
}

func TestIndexedAdapter_ListLandmarks(t *testing.T) {
	a, momDir := setupIndexedAdapter(t)

	lm := indexedTestDoc("landmark-x")
	lm.Landmark = true
	cs := 0.8
	lm.CentralityScore = &cs
	a.Write(lm) //nolint:errcheck

	normal := indexedTestDoc("normal-y")
	normal.Landmark = false
	a.Write(normal) //nolint:errcheck

	results, err := a.ListLandmarks([]string{momDir}, 10)
	if err != nil {
		t.Fatalf("ListLandmarks failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 landmark, got %d", len(results))
	}
	if results[0].ID != "landmark-x" {
		t.Errorf("expected landmark-x, got %q", results[0].ID)
	}
}

func TestIndexedAdapter_CountDivergenceTriggersReindex(t *testing.T) {
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	os.MkdirAll(filepath.Join(momDir, "memory"), 0755) //nolint:errcheck

	// First adapter: write 2 docs.
	a1 := NewIndexedAdapter(momDir)
	a1.Write(indexedTestDoc("count-a")) //nolint:errcheck
	a1.Write(indexedTestDoc("count-b")) //nolint:errcheck
	a1.Close()                          //nolint:errcheck

	// Write a third doc directly to JSON (bypass index).
	jsonA := NewJSONAdapter(momDir)
	jsonA.Write(indexedTestDoc("count-c")) //nolint:errcheck

	// Second adapter: should detect count divergence (2 in DB, 3 in JSON)
	// and auto-reindex.
	a2 := NewIndexedAdapter(momDir)
	defer a2.Close()

	results, err := a2.Search(SearchOptions{Query: "", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	// After auto-reindex, all 3 docs should be present.
	if len(results) != 3 {
		t.Errorf("expected 3 docs after auto-reindex, got %d", len(results))
	}
}
