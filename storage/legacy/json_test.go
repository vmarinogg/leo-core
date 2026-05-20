package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testDoc(id string) *Doc {
	return &Doc{
		ID:        id,
		Scope:     "project",
		Tags:      []string{"test"},
		Created:   time.Now().UTC(),
		CreatedBy: "test",
		Content:   map[string]any{"fact": "test fact"},
	}
}

func setupAdapter(t *testing.T) (*JSONAdapter, string) {
	t.Helper()
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	os.MkdirAll(filepath.Join(momDir, "memory"), 0755)
	return NewJSONAdapter(momDir), momDir
}

func TestJSONAdapter_WriteAndRead(t *testing.T) {
	adapter, _ := setupAdapter(t)
	doc := testDoc("test-doc")

	if err := adapter.Write(doc); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	got, err := adapter.Read("test-doc")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if got.ID != "test-doc" {
		t.Errorf("expected id %q, got %q", "test-doc", got.ID)
	}
}

func TestJSONAdapter_WriteValidation(t *testing.T) {
	adapter, _ := setupAdapter(t)
	doc := &Doc{
		ID: "INVALID_ID",
	}

	if err := adapter.Write(doc); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestJSONAdapter_Delete(t *testing.T) {
	adapter, _ := setupAdapter(t)
	doc := testDoc("to-delete")

	adapter.Write(doc)

	if err := adapter.Delete("to-delete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := adapter.Read("to-delete"); err == nil {
		t.Fatal("expected error reading deleted doc, got nil")
	}
}

func TestJSONAdapter_Query(t *testing.T) {
	adapter, _ := setupAdapter(t)

	docA := testDoc("doc-a")
	docA.Tags = []string{"alpha"}
	adapter.Write(docA)

	docB := testDoc("doc-b")
	docB.Tags = []string{"beta"}
	docB.Content = map[string]any{
		"rule": "test rule", "why": "test", "how_to_apply": []any{"test"},
		"responsibility": "test",
	}
	adapter.Write(docB)

	docs, err := adapter.Query(QueryFilter{Tags: []string{"alpha"}})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].ID != "doc-a" {
		t.Errorf("expected doc-a, got %s", docs[0].ID)
	}
}

func TestJSONAdapter_RebuildIndex(t *testing.T) {
	adapter, _ := setupAdapter(t)
	adapter.Write(testDoc("indexed-doc"))

	idx, err := adapter.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if ids, ok := idx.ByTag["test"]; !ok || len(ids) == 0 {
		t.Fatal("expected test tag in index")
	}
}

func TestJSONAdapter_BulkWrite(t *testing.T) {
	adapter, _ := setupAdapter(t)

	docs := []*Doc{testDoc("bulk-a"), testDoc("bulk-b"), testDoc("bulk-c")}
	if err := adapter.BulkWrite(docs); err != nil {
		t.Fatalf("BulkWrite failed: %v", err)
	}

	idx, err := adapter.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if ids := idx.ByScope["project"]; len(ids) != 3 {
		t.Fatalf("expected 3 docs in index, got %d", len(ids))
	}
}

func TestJSONAdapter_Read_NotFound(t *testing.T) {
	adapter, _ := setupAdapter(t)

	if _, err := adapter.Read("nonexistent"); err == nil {
		t.Fatal("expected error for missing doc")
	}
}

func TestJSONAdapter_Delete_NotFound(t *testing.T) {
	adapter, _ := setupAdapter(t)

	if err := adapter.Delete("nonexistent"); err == nil {
		t.Fatal("expected error for deleting missing doc")
	}
}

func TestJSONAdapter_List_EmptyKB(t *testing.T) {
	adapter, _ := setupAdapter(t)

	idx, err := adapter.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if idx.Version != "1" {
		t.Errorf("expected version %q, got %q", "1", idx.Version)
	}
}

func TestJSONAdapter_Query_ByTags(t *testing.T) {
	adapter, _ := setupAdapter(t)

	docA := testDoc("tagged-a")
	docA.Tags = []string{"alpha", "beta"}
	adapter.Write(docA)

	docB := testDoc("tagged-b")
	docB.Tags = []string{"beta", "gamma"}
	adapter.Write(docB)

	docC := testDoc("tagged-c")
	docC.Tags = []string{"gamma"}
	adapter.Write(docC)

	// Query by single tag.
	docs, err := adapter.Query(QueryFilter{Tags: []string{"alpha"}})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != "tagged-a" {
		t.Errorf("expected tagged-a, got %v", docs)
	}

	// Query by shared tag.
	docs, err = adapter.Query(QueryFilter{Tags: []string{"beta"}})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs for beta, got %d", len(docs))
	}
}

func TestJSONAdapter_Query_CombinedFilters(t *testing.T) {
	adapter, _ := setupAdapter(t)

	docA := testDoc("combo-a")
	docA.Scope = "core"
	adapter.Write(docA)

	docB := testDoc("combo-b")
	docB.Scope = "project"
	adapter.Write(docB)

	// Filter by scope + tags.
	docs, err := adapter.Query(QueryFilter{Scope: "core", Tags: []string{"test"}})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != "combo-a" {
		t.Errorf("expected combo-a, got %v", docs)
	}
}

func TestJSONAdapter_Query_NoFilter(t *testing.T) {
	adapter, _ := setupAdapter(t)

	adapter.Write(testDoc("all-a"))
	adapter.Write(testDoc("all-b"))

	docs, err := adapter.Query(QueryFilter{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs, got %d", len(docs))
	}
}

func TestJSONAdapter_Write_Overwrite(t *testing.T) {
	adapter, _ := setupAdapter(t)

	doc := testDoc("overwrite-me")
	doc.Content = map[string]any{"fact": "original"}
	adapter.Write(doc)

	doc.Content = map[string]any{"fact": "updated"}
	adapter.Write(doc)

	got, err := adapter.Read("overwrite-me")
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got.Content["fact"] != "updated" {
		t.Errorf("expected updated content, got %v", got.Content["fact"])
	}
}

func TestJSONAdapter_BulkWrite_ValidationFailure(t *testing.T) {
	adapter, _ := setupAdapter(t)

	docs := []*Doc{
		testDoc("good-doc"),
		{ID: "BAD_ID"}, // invalid — no required fields
	}

	if err := adapter.BulkWrite(docs); err == nil {
		t.Fatal("expected validation error in BulkWrite")
	}
}

func TestJSONAdapter_Index_TagConnections(t *testing.T) {
	adapter, _ := setupAdapter(t)

	doc := testDoc("connected")
	doc.Tags = []string{"alpha", "beta", "gamma"}
	adapter.Write(doc)

	idx, err := adapter.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	for _, tag := range []string{"alpha", "beta", "gamma"} {
		if ids, ok := idx.ByTag[tag]; !ok || len(ids) == 0 {
			t.Errorf("expected tag %q in index", tag)
		}
	}
}
