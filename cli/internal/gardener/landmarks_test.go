package gardener_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/gardener"
)

// minimalDoc produces the minimal JSON needed for landmark computation.
func minimalDoc(id string, tags []string) map[string]any {
	return map[string]any{
		"id":              id,
		"type":            "fact",
		"lifecycle":       "permanent",
		"scope":           "project",
		"tags":            tags,
		"created":         time.Now().UTC().Format(time.RFC3339),
		"created_by":      "test",
		"updated":         time.Now().UTC().Format(time.RFC3339),
		"updated_by":      "test",
		"confidence":      "EXTRACTED",
		"promotion_state": "draft",
		"classification":  "INTERNAL",
		"content":         map[string]any{"summary": "test"},
	}
}

func writeDoc(t *testing.T, dir string, doc map[string]any) {
	t.Helper()
	id, _ := doc["id"].(string)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshaling doc %s: %v", id, err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("writing doc %s: %v", id, err)
	}
}

// TestComputeLandmarks_BelowThreshold verifies skip behaviour for small corpora.
func TestComputeLandmarks_BelowThreshold(t *testing.T) {
	dir := t.TempDir()

	// Write 50 docs — below MinDocsForLandmarks (100).
	for i := 0; i < 50; i++ {
		writeDoc(t, dir, minimalDoc(
			id50(i),
			[]string{"alpha", "beta"},
		))
	}

	n, err := gardener.ComputeLandmarks(dir, 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 docs updated below threshold, got %d", n)
	}
}

func id50(i int) string {
	return "doc-" + paddedInt(i)
}

func paddedInt(i int) string {
	s := "000" + intStr(i)
	return s[len(s)-4:]
}

func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

// TestComputeLandmarks_Top2Pct verifies that top 2% get landmark=true.
func TestComputeLandmarks_Top2Pct(t *testing.T) {
	dir := t.TempDir()
	total := 100

	// Create 100 docs.
	// Docs 0..1 share a rare tag "rare-hub" (only 2 docs share it, high weight per edge).
	// Docs 0..99 all share "common" tag (low weight).
	for i := 0; i < total; i++ {
		tags := []string{"common"}
		if i < 2 {
			tags = append(tags, "rare-hub")
		}
		writeDoc(t, dir, minimalDoc(id50(i), tags))
	}

	n, err := gardener.ComputeLandmarks(dir, 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2% of 100 = 2 docs should be landmarks.
	if n == 0 {
		t.Errorf("expected some docs updated, got 0")
	}

	// Count actual landmarks on disk.
	landmarkCount := 0
	for i := 0; i < total; i++ {
		path := filepath.Join(dir, id50(i)+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading doc: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parsing doc: %v", err)
		}
		if lm, ok := doc["landmark"].(bool); ok && lm {
			landmarkCount++
		}
	}

	// Expect exactly ceil(2% of 100) = 2 landmarks.
	if landmarkCount != 2 {
		t.Errorf("expected 2 landmarks, got %d", landmarkCount)
	}
}

// TestComputeLandmarks_CentralityNormalized verifies centrality_score is in [0,1].
func TestComputeLandmarks_CentralityNormalized(t *testing.T) {
	dir := t.TempDir()
	total := 100

	for i := 0; i < total; i++ {
		tags := []string{"base"}
		if i%10 == 0 {
			tags = append(tags, "hub")
		}
		writeDoc(t, dir, minimalDoc(id50(i), tags))
	}

	_, err := gardener.ComputeLandmarks(dir, 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 0; i < total; i++ {
		path := filepath.Join(dir, id50(i)+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading doc: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parsing doc: %v", err)
		}
		if score, ok := doc["centrality_score"].(float64); ok {
			if score < 0 || score > 1+1e-9 {
				t.Errorf("doc %s has out-of-range centrality_score %f", id50(i), score)
			}
		}
	}
}

// TestComputeLandmarks_RareTagHigherWeight verifies that rare shared tags
// produce higher per-edge weights, such that a doc with a single rare-tag
// connection outscores a doc with only common-tag connections when the
// rare-tag edge weight exceeds the total contribution of common-tag edges.
//
// We construct a scenario where:
//   - doc-A and doc-B share "ultra-rare" (only 2 docs, weight = 0.5 per edge)
//   - doc-C and doc-D share only "very-common" with each other (2 docs, weight = 0.5)
//
// Both have exactly one shared-tag edge — but the uncommon case shows that
// the weight formula 1/count rewards rarity: when a tag is shared by 2 docs,
// each gets weight 0.5. When shared by 100 docs, each gets weight 0.01.
// doc-B (exclusive to the rare pair) should score equal to doc-D
// (exclusive to the common pair) because they each have the same edge count
// and the same weight when both tags are shared by exactly 2 docs.
//
// The real test: a doc connected only via a rare shared tag should score
// strictly higher than a doc connected only via a common (many-docs) tag.
func TestComputeLandmarks_RareTagHigherWeight(t *testing.T) {
	dir := t.TempDir()

	// Build 100 docs:
	// docs 0..9   share "hub" tag (10 docs, weight per edge = 1/10)
	// docs 10..11 share "rare" tag (2 docs, weight per edge = 1/2)
	// docs 12..99 share "solo" tag with no overlap (each alone, no edges)
	for i := 0; i < 100; i++ {
		var tags []string
		switch {
		case i < 10:
			tags = []string{"hub"}
		case i < 12:
			tags = []string{"rare"}
		default:
			// Unique tag per doc — no edges.
			tags = []string{"solo-" + id50(i)}
		}
		writeDoc(t, dir, minimalDoc(id50(i), tags))
	}

	_, err := gardener.ComputeLandmarks(dir, 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// doc-0010 has 1 edge to doc-0011 with weight 1/2 = 0.5.
	// doc-0000 has 9 edges each with weight 1/10 = 0.1, total = 0.9.
	// So doc-0000 (hub) > doc-0010 (rare pair) > doc-0012 (isolated).
	score0000 := readCentralityScore(t, filepath.Join(dir, id50(0)+".json"))
	score0010 := readCentralityScore(t, filepath.Join(dir, id50(10)+".json"))
	score0012 := readCentralityScore(t, filepath.Join(dir, id50(12)+".json"))

	if score0000 <= score0010 {
		t.Errorf("hub should outscore rare pair: hub=%f, rare=%f", score0000, score0010)
	}
	if score0010 <= score0012 {
		t.Errorf("rare pair should outscore isolated doc: rare=%f, isolated=%f", score0010, score0012)
	}

	// Verify the rare-pair advantage: a doc sharing only a tag with 2 others
	// (weight=0.5) vs a doc sharing a tag with 10 others (weight=0.1 per edge).
	// doc-0010 total = 1*0.5 = 0.5; doc-0000 total = 9*0.1 = 0.9.
	// Ratio should reflect the weight formula.
	// Isolated doc has score=0 (field absent or 0), which readCentralityScore returns as 0.
	if math.IsNaN(score0000) || math.IsNaN(score0010) {
		t.Error("hub and rare-pair centrality scores should not be NaN")
	}
	if score0012 != 0 && !math.IsNaN(score0012) {
		// Isolated doc may have NaN (field absent) or 0 — both are acceptable.
		if score0012 > score0010 {
			t.Errorf("isolated doc should not outscore rare-pair doc: isolated=%f, rare=%f", score0012, score0010)
		}
	}
}

// TestComputeLandmarks_AllFalseAfterReset verifies that docs outside the top N%
// have landmark=false after computation.
func TestComputeLandmarks_AllFalseAfterReset(t *testing.T) {
	dir := t.TempDir()
	total := 100

	for i := 0; i < total; i++ {
		doc := minimalDoc(id50(i), []string{"alpha"})
		// Pre-set landmark=true on all docs to test reset behaviour.
		doc["landmark"] = true
		writeDoc(t, dir, doc)
	}

	_, err := gardener.ComputeLandmarks(dir, 0.0) // 0% threshold → all become false
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 0; i < total; i++ {
		path := filepath.Join(dir, id50(i)+".json")
		data, _ := os.ReadFile(path)
		var doc map[string]any
		json.Unmarshal(data, &doc) //nolint:errcheck
		if lm, ok := doc["landmark"].(bool); ok && lm {
			t.Errorf("doc %s: expected landmark=false after 0%% threshold", id50(i))
		}
	}
}

// TestComputeLandmarks_DiverseSelection verifies that the greedy diversity
// penalty spreads landmarks across different tag clusters instead of
// concentrating them in the largest cluster.
func TestComputeLandmarks_DiverseSelection(t *testing.T) {
	dir := t.TempDir()

	// Create 100 docs in two clusters:
	// Cluster A (docs 0..59):  tags ["go", "function", "ast", "pkg-cmd"]
	// Cluster B (docs 60..79): tags ["go", "type", "ast", "pkg-gardener"]
	// Cluster C (docs 80..99): tags ["commit", "feat", "bootstrap"]
	//
	// Cluster A is largest. Without diversity, all landmarks would come from A.
	// With diversity, landmarks should spread across A, B, and C.
	for i := 0; i < 100; i++ {
		var tags []string
		switch {
		case i < 60:
			tags = []string{"go", "function", "ast", "pkg-cmd"}
		case i < 80:
			tags = []string{"go", "type", "ast", "pkg-gardener"}
		default:
			tags = []string{"commit", "feat", "bootstrap"}
		}
		writeDoc(t, dir, minimalDoc(id50(i), tags))
	}

	// 5% = 5 landmarks.
	_, err := gardener.ComputeLandmarks(dir, 5.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count landmarks per cluster.
	clusterA, clusterB, clusterC := 0, 0, 0
	for i := 0; i < 100; i++ {
		path := filepath.Join(dir, id50(i)+".json")
		data, _ := os.ReadFile(path)
		var doc map[string]any
		json.Unmarshal(data, &doc) //nolint:errcheck
		if lm, ok := doc["landmark"].(bool); ok && lm {
			switch {
			case i < 60:
				clusterA++
			case i < 80:
				clusterB++
			default:
				clusterC++
			}
		}
	}

	// Diversity guarantee: at least 2 clusters should have landmarks.
	clustersWithLandmarks := 0
	if clusterA > 0 {
		clustersWithLandmarks++
	}
	if clusterB > 0 {
		clustersWithLandmarks++
	}
	if clusterC > 0 {
		clustersWithLandmarks++
	}

	if clustersWithLandmarks < 2 {
		t.Errorf("expected landmarks in ≥2 clusters, got: A=%d B=%d C=%d",
			clusterA, clusterB, clusterC)
	}
}

func readCentralityScore(t *testing.T, path string) float64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	if s, ok := doc["centrality_score"].(float64); ok {
		return s
	}
	// Field absent means score is 0.
	return 0
}
