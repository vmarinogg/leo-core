package drafter

import (
	"strings"
	"testing"
)

// TestRAKE verifies keyword extraction from sample text.
func TestRAKE(t *testing.T) {
	text := "The memory oriented machine processes raw conversation data into structured memory documents using keyword extraction algorithms."
	candidates := RAKE(text, 5)

	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate, got 0")
	}

	// All candidates should have a positive score.
	for _, c := range candidates {
		if c.Score <= 0 {
			t.Errorf("candidate %q has non-positive score %f", c.Phrase, c.Score)
		}
		if c.Phrase == "" {
			t.Error("candidate phrase must not be empty")
		}
	}

	// Scores should be sorted descending.
	for i := 1; i < len(candidates); i++ {
		if candidates[i].Score > candidates[i-1].Score {
			t.Errorf("candidates not sorted: [%d].Score=%f > [%d].Score=%f",
				i, candidates[i].Score, i-1, candidates[i-1].Score)
		}
	}

	// Should not exceed topN.
	if len(candidates) > 5 {
		t.Errorf("expected at most 5 candidates, got %d", len(candidates))
	}
}

// TestRAKEEmpty verifies RAKE handles empty input gracefully.
func TestRAKEEmpty(t *testing.T) {
	candidates := RAKE("", 5)
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for empty input, got %d", len(candidates))
	}
}

// TestRAKEStopwordsOnly verifies RAKE returns nothing for stopword-only text.
func TestRAKEStopwordsOnly(t *testing.T) {
	candidates := RAKE("the a an is are was were be", 5)
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for stopword-only text, got %d", len(candidates))
	}
}

// TestExtractFileTags verifies tag extraction from file paths.
func TestExtractFileTags(t *testing.T) {
	paths := []string{
		"cli/internal/drafter/drafter.go",
		"cli/internal/recorder/recorder.go",
		".mom/memory/fact-001.json",
	}
	tags := ExtractFileTags(paths)

	if len(tags) == 0 {
		t.Fatal("expected tags, got none")
	}

	// Should find meaningful path components.
	found := make(map[string]bool)
	for _, tag := range tags {
		found[tag] = true
	}

	expected := []string{"cli", "internal", "drafter", "recorder"}
	for _, e := range expected {
		if !found[e] {
			t.Errorf("expected tag %q not found in %v", e, tags)
		}
	}

	// No duplicates.
	seen := make(map[string]bool)
	for _, tag := range tags {
		if seen[tag] {
			t.Errorf("duplicate tag %q", tag)
		}
		seen[tag] = true
	}
}

// TestExtractFileTags_Empty verifies empty input returns empty slice.
func TestExtractFileTags_Empty(t *testing.T) {
	tags := ExtractFileTags(nil)
	if len(tags) != 0 {
		t.Errorf("expected 0 tags for nil input, got %d", len(tags))
	}
}

// TestExtractIdentifiers verifies CamelCase and snake_case extraction.
func TestExtractIdentifiers(t *testing.T) {
	text := "The BM25Index uses newBM25Index to rank_candidates and extract_file_tags from RakeCandidate results."
	ids := ExtractIdentifiers(text)

	if len(ids) == 0 {
		t.Fatal("expected identifiers, got none")
	}

	found := make(map[string]bool)
	for _, id := range ids {
		found[id] = true
	}

	// snake_case identifiers are converted to kebab-case.
	for _, expected := range []string{"rank-candidates", "extract-file-tags"} {
		if !found[expected] {
			t.Errorf("expected identifier %q not found", expected)
		}
	}
}

// TestExtractIdentifiers_Empty verifies empty input.
func TestExtractIdentifiers_Empty(t *testing.T) {
	ids := ExtractIdentifiers("")
	if len(ids) != 0 {
		t.Errorf("expected 0 ids for empty input, got %d", len(ids))
	}
}

// TestDetectBoundaries verifies chunk splitting on context divergence.
func TestDetectBoundaries(t *testing.T) {
	turns := []Turn{
		{Text: "working on drafter", FilePaths: []string{"cli/internal/drafter/drafter.go"}, Keywords: []string{"drafter", "pipeline"}},
		{Text: "more drafter work", FilePaths: []string{"cli/internal/drafter/bm25.go"}, Keywords: []string{"bm25", "ranking"}},
		// New topic entirely — different files and keywords.
		{Text: "fixing CI pipeline", FilePaths: []string{".github/workflows/ci.yml"}, Keywords: []string{"ci", "github", "workflow"}},
		{Text: "updating release", FilePaths: []string{".github/workflows/release.yml"}, Keywords: []string{"release", "goreleaser"}},
	}

	chunks := DetectBoundaries(turns, 0.6)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Each chunk must cover a valid range.
	for i, ch := range chunks {
		if ch.StartIdx >= ch.EndIdx {
			t.Errorf("chunk %d: StartIdx=%d >= EndIdx=%d", i, ch.StartIdx, ch.EndIdx)
		}
		if ch.StartIdx < 0 || ch.EndIdx > len(turns) {
			t.Errorf("chunk %d: indices out of range [%d, %d]", i, ch.StartIdx, ch.EndIdx)
		}
	}

	// All turns must be covered.
	covered := make([]bool, len(turns))
	for _, ch := range chunks {
		for j := ch.StartIdx; j < ch.EndIdx; j++ {
			covered[j] = true
		}
	}
	for i, c := range covered {
		if !c {
			t.Errorf("turn %d not covered by any chunk", i)
		}
	}
}

// TestDetectBoundaries_Empty handles empty input.
func TestDetectBoundaries_Empty(t *testing.T) {
	chunks := DetectBoundaries(nil, 0.6)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for nil input, got %d", len(chunks))
	}
}

// TestDetectBoundaries_Single handles a single turn.
func TestDetectBoundaries_Single(t *testing.T) {
	turns := []Turn{
		{Text: "hello", Keywords: []string{"hello"}},
	}
	chunks := DetectBoundaries(turns, 0.6)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].StartIdx != 0 || chunks[0].EndIdx != 1 {
		t.Errorf("unexpected chunk range: [%d, %d]", chunks[0].StartIdx, chunks[0].EndIdx)
	}
}

// TestBM25Index verifies scoring and ranking.
func TestBM25Index(t *testing.T) {
	vocab := []string{
		"drafter pipeline",
		"bm25 ranking",
		"rake keyword extraction",
		"memory documents",
		"raw recording",
	}
	idx := newBM25Index(vocab)

	// Score a query against a document.
	score := idx.score("drafter", tokenizeBM25("drafter pipeline"))
	if score <= 0 {
		t.Errorf("expected positive score for matching query, got %f", score)
	}

	// Non-matching query should score lower.
	noMatchScore := idx.score("drafter", tokenizeBM25("raw recording"))
	if noMatchScore >= score {
		t.Errorf("non-matching doc scored higher (%f >= %f)", noMatchScore, score)
	}
}

// TestBM25Index_RankCandidates verifies ranking order.
func TestBM25Index_RankCandidates(t *testing.T) {
	vocab := []string{
		"drafter pipeline",
		"bm25 ranking algorithm",
		"keyword extraction",
	}
	idx := newBM25Index(vocab)

	candidates := []RakeCandidate{
		{Phrase: "drafter pipeline", Score: 3.0},
		{Phrase: "unrelated concept", Score: 0.5},
	}

	ranked := idx.rankCandidates(candidates)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked results, got %d", len(ranked))
	}

	// "drafter pipeline" should rank first.
	if !strings.Contains(ranked[0], "drafter") {
		t.Errorf("expected drafter-related phrase to rank first, got %q", ranked[0])
	}
}

// TestBM25Index_Empty handles empty vocab.
func TestBM25Index_Empty(t *testing.T) {
	idx := newBM25Index(nil)
	score := idx.score("anything", []string{"word"})
	if score != 0 {
		t.Errorf("expected 0 score with empty index, got %f", score)
	}
	ranked := idx.rankCandidates([]RakeCandidate{{Phrase: "hello", Score: 1.0}})
	if len(ranked) != 1 {
		t.Errorf("expected 1 result from empty-vocab ranking, got %d", len(ranked))
	}
}
