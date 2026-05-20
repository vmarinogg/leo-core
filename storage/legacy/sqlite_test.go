package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setupSQLiteIndex creates a temporary .mom/ dir and opens a sqliteIndex.
func setupSQLiteIndex(t *testing.T) (*sqliteIndex, string) {
	t.Helper()
	dir := t.TempDir()
	momDir := filepath.Join(dir, ".mom")
	if err := os.MkdirAll(filepath.Join(momDir, "cache"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	idx, err := openSQLiteIndex(momDir)
	if err != nil {
		t.Fatalf("openSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx, momDir
}

func testStorageDoc(id string) *Doc {
	return &Doc{
		ID:             id,
		Scope:          "project",
		Tags:           []string{"test", "memory"},
		Created:        time.Now().UTC(),
		CreatedBy:      "test",
		PromotionState: "curated",
		Classification: "INTERNAL",
		Content:        map[string]any{"summary": "A test memory document about " + id},
	}
}

func TestSQLiteIndex_OpenAndClose(t *testing.T) {
	idx, _ := setupSQLiteIndex(t)
	if err := idx.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Double-close should not panic.
	idx.db = nil
	if err := idx.Close(); err != nil {
		t.Fatalf("nil Close failed: %v", err)
	}
}

func TestSQLiteIndex_UpsertAndSearch(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	doc := testStorageDoc("auth-decision")
	doc.Content["summary"] = "Authentication decisions use JWT tokens for stateless sessions"
	doc.Tags = []string{"auth", "jwt", "decision"}

	if err := idx.Upsert(doc, momDir); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	results, err := idx.Search(SearchOptions{Query: "jwt authentication", Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result, got 0")
	}
	if results[0].ID != "auth-decision" {
		t.Errorf("expected auth-decision, got %q", results[0].ID)
	}
}

func TestSQLiteIndex_EmptyQuery_ReturnsAll(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	for _, id := range []string{"doc-a", "doc-b", "doc-c"} {
		if err := idx.Upsert(testStorageDoc(id), momDir); err != nil {
			t.Fatalf("Upsert %q: %v", id, err)
		}
	}

	results, err := idx.Search(SearchOptions{Query: "", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestSQLiteIndex_Delete(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	doc := testStorageDoc("to-delete")
	idx.Upsert(doc, momDir) //nolint:errcheck

	if err := idx.Delete("to-delete"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	results, err := idx.Search(SearchOptions{Query: "to-delete", Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, r := range results {
		if r.ID == "to-delete" {
			t.Error("deleted doc still appears in search results")
		}
	}
}

func TestSQLiteIndex_ExcludeDrafts(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	// Insert one curated and one draft doc.
	curated := testStorageDoc("curated-doc")
	curated.Content["summary"] = "Curated memory about architecture patterns"
	curated.Tags = []string{"architecture", "pattern"}
	curated.PromotionState = "curated"
	idx.Upsert(curated, momDir) //nolint:errcheck

	draft := testStorageDoc("draft-doc")
	draft.Content["summary"] = "Draft memory about architecture patterns"
	draft.Tags = []string{"architecture", "draft"}
	draft.PromotionState = "draft"
	idx.Upsert(draft, momDir) //nolint:errcheck

	// Search with ExcludeDrafts=true should only return curated.
	results, err := idx.Search(SearchOptions{Query: "architecture", ExcludeDrafts: true, Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, r := range results {
		if r.PromotionState == "draft" {
			t.Errorf("draft doc %q appeared in results with ExcludeDrafts=true", r.ID)
		}
	}

	// Search without ExcludeDrafts should return both.
	all, err := idx.Search(SearchOptions{Query: "architecture", ExcludeDrafts: false, Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(all) < 2 {
		t.Errorf("expected at least 2 results without draft filter, got %d", len(all))
	}
}

func TestSQLiteIndex_TagFilter(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	docA := testStorageDoc("tagged-a")
	docA.Tags = []string{"golang", "backend"}
	docA.Content["summary"] = "Go backend patterns"
	idx.Upsert(docA, momDir) //nolint:errcheck

	docB := testStorageDoc("tagged-b")
	docB.Tags = []string{"python", "backend"}
	docB.Content["summary"] = "Python backend patterns"
	idx.Upsert(docB, momDir) //nolint:errcheck

	// Filter by "golang" tag — should return only tagged-a.
	results, err := idx.Search(SearchOptions{Query: "", Tags: []string{"golang"}, Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "tagged-a" {
		t.Errorf("expected tagged-a, got %q", results[0].ID)
	}
}

func TestSQLiteIndex_ScopeFilter(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	otherScope := "/other/.mom"

	docA := testStorageDoc("scope-a")
	docA.Content["summary"] = "Doc in primary scope"
	idx.Upsert(docA, momDir) //nolint:errcheck

	docB := testStorageDoc("scope-b")
	docB.Content["summary"] = "Doc in other scope"
	idx.Upsert(docB, otherScope) //nolint:errcheck

	// Filter to primary scope only.
	results, err := idx.Search(SearchOptions{Query: "", ScopePaths: []string{momDir}, Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, r := range results {
		if r.ScopePath != momDir {
			t.Errorf("result %q from unexpected scope %q", r.ID, r.ScopePath)
		}
	}
}

func TestSQLiteIndex_LandmarkBoost(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	normal := testStorageDoc("normal-doc")
	normal.Content["summary"] = "Regular memory about search algorithms"
	normal.Tags = []string{"search"}
	normal.Landmark = false
	idx.Upsert(normal, momDir) //nolint:errcheck

	landmark := testStorageDoc("landmark-doc")
	landmark.Content["summary"] = "Landmark memory about search algorithms"
	landmark.Tags = []string{"search"}
	landmark.Landmark = true
	cs := 0.9
	landmark.CentralityScore = &cs
	idx.Upsert(landmark, momDir) //nolint:errcheck

	results, err := idx.Search(SearchOptions{Query: "search algorithms", Limit: 10})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Landmark doc should have a higher score due to boost.
	landmarkScore := 0.0
	normalScore := 0.0
	for _, r := range results {
		if r.ID == "landmark-doc" {
			landmarkScore = r.Score
		}
		if r.ID == "normal-doc" {
			normalScore = r.Score
		}
	}
	if landmarkScore <= normalScore {
		t.Errorf("landmark score (%f) should be > normal score (%f)", landmarkScore, normalScore)
	}
}

func TestSQLiteIndex_CountByScope(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	for _, id := range []string{"doc-1", "doc-2", "doc-3"} {
		idx.Upsert(testStorageDoc(id), momDir) //nolint:errcheck
	}

	count, err := idx.CountByScope(momDir)
	if err != nil {
		t.Fatalf("CountByScope failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3, got %d", count)
	}
}

func TestSQLiteIndex_BulkUpsert(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	docs := []*Doc{
		testStorageDoc("bulk-a"),
		testStorageDoc("bulk-b"),
		testStorageDoc("bulk-c"),
	}
	if err := idx.BulkUpsert(docs, momDir); err != nil {
		t.Fatalf("BulkUpsert failed: %v", err)
	}

	count, err := idx.CountByScope(momDir)
	if err != nil {
		t.Fatalf("CountByScope failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3, got %d", count)
	}
}

func TestSQLiteIndex_ReindexScope(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	// Insert initial docs.
	docs := []*Doc{testStorageDoc("old-a"), testStorageDoc("old-b")}
	idx.BulkUpsert(docs, momDir) //nolint:errcheck

	// Reindex with different set.
	newDocs := []*Doc{
		testStorageDoc("new-x"),
		testStorageDoc("new-y"),
		testStorageDoc("new-z"),
	}
	if err := idx.ReindexScope(momDir, newDocs); err != nil {
		t.Fatalf("ReindexScope failed: %v", err)
	}

	count, err := idx.CountByScope(momDir)
	if err != nil {
		t.Fatalf("CountByScope failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 after reindex, got %d", count)
	}

	// Old docs should be gone.
	results, err := idx.Search(SearchOptions{Query: "", Limit: 20})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	for _, r := range results {
		if r.ID == "old-a" || r.ID == "old-b" {
			t.Errorf("old doc %q still present after reindex", r.ID)
		}
	}
}

func TestSQLiteIndex_ListLandmarks(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	// Insert mix of landmark and normal docs.
	scores := []float64{0.8, 0.5, 0.9}
	for i, id := range []string{"lm-a", "lm-b", "lm-c"} {
		doc := testStorageDoc(id)
		doc.Landmark = true
		s := scores[i]
		doc.CentralityScore = &s
		idx.Upsert(doc, momDir) //nolint:errcheck
	}
	normal := testStorageDoc("normal-x")
	normal.Landmark = false
	idx.Upsert(normal, momDir) //nolint:errcheck

	results, err := idx.ListLandmarks([]string{momDir}, 10)
	if err != nil {
		t.Fatalf("ListLandmarks failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 landmarks, got %d", len(results))
	}
	// Should be sorted by centrality descending.
	if results[0].ID != "lm-c" { // score 0.9
		t.Errorf("expected lm-c first (highest centrality), got %q", results[0].ID)
	}
}

func TestSQLiteIndex_Upsert_Overwrite(t *testing.T) {
	idx, momDir := setupSQLiteIndex(t)

	doc := testStorageDoc("overwrite-me")
	doc.Content["summary"] = "Original content"
	idx.Upsert(doc, momDir) //nolint:errcheck

	doc.Content["summary"] = "Updated content"
	if err := idx.Upsert(doc, momDir); err != nil {
		t.Fatalf("Upsert overwrite failed: %v", err)
	}

	results, err := idx.Search(SearchOptions{Query: "updated content", Limit: 5})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected to find overwritten doc")
	}
}

func TestBuildFTSQueryOR(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello world", `"hello" "world"`},
		{"JWT authentication", `"jwt" "authentication"`},
		{"", ""},
		{"single", `"single"`},
	}
	for _, tc := range cases {
		got := buildFTSQueryOR(tc.input)
		if got != tc.want {
			t.Errorf("buildFTSQueryOR(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBuildFTSQueryAND(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello world", `+"hello" +"world"`},
		{"JWT authentication", `+"jwt" +"authentication"`},
		{"", ""},
		{"single", `+"single"`},
	}
	for _, tc := range cases {
		got := buildFTSQueryAND(tc.input)
		if got != tc.want {
			t.Errorf("buildFTSQueryAND(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSearchColumnWeights(t *testing.T) {
	dir := t.TempDir()
	idx, err := openSQLiteIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// contentMatch has the query term only in content_text (high weight).
	contentMatch := &Doc{
		ID: "content-match", Scope: "project", Tags: []string{"unrelated"},
		Created: time.Now(), CreatedBy: "test", PromotionState: "curated",
		Classification: "INTERNAL", Compartments: map[string][]string{},
		Content: map[string]any{"detail": "harness integration architecture decision"},
	}
	// tagMatch has the query term only in tags (low weight).
	tagMatch := &Doc{
		ID: "tag-match", Scope: "project", Tags: []string{"harness"},
		Created: time.Now(), CreatedBy: "test", PromotionState: "curated",
		Classification: "INTERNAL", Compartments: map[string][]string{},
		Content: map[string]any{"detail": "unrelated information about something else"},
	}

	if err := idx.Upsert(contentMatch, dir); err != nil {
		t.Fatal(err)
	}
	if err := idx.Upsert(tagMatch, dir); err != nil {
		t.Fatal(err)
	}

	results, err := idx.Search(SearchOptions{Query: "harness", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// content-match should rank first — content_text weight (10) > tags weight (1).
	if results[0].ID != "content-match" {
		t.Errorf("expected content-match to rank first (content weight > tag weight), got %q", results[0].ID)
	}
}
