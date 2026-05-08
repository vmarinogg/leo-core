package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IndexedAdapter wraps a JSONAdapter with a SQLite+FTS5 index.
// JSON files remain the source of truth; SQLite is a rebuildable cache.
//
// If the SQLite index is unavailable (open error, I/O error), the adapter
// transparently falls back to the underlying JSONAdapter without error.
//
// Write-through: every Write/Delete/BulkWrite goes to both JSON and SQLite.
// On startup the adapter checks for count divergence and reindexes as needed.
type IndexedAdapter struct {
	json      *JSONAdapter
	idx       *sqliteIndex // nil when degraded
	momDir    string
	scopePath string // .mom/ absolute path (= momDir for write ops)
}

// NewIndexedAdapter creates an IndexedAdapter for the given .mom/ directory.
// It opens (or creates) the SQLite index and runs a startup count-check to
// detect external changes (git pull, manual edits). If SQLite is unavailable
// the adapter degrades gracefully to JSONAdapter-only mode.
func NewIndexedAdapter(momDir string) *IndexedAdapter {
	a := &IndexedAdapter{
		json:      NewJSONAdapter(momDir),
		momDir:    momDir,
		scopePath: momDir,
	}

	idx, err := openSQLiteIndex(momDir)
	if err != nil {
		// Graceful degradation: log to stderr and continue without index.
		fmt.Fprintf(os.Stderr, "[mom] SQLite index unavailable (%v) — falling back to full scan\n", err)
		return a
	}
	a.idx = idx

	// Startup count check: if JSON file count != DB count, reindex.
	a.checkAndReindex()

	return a
}

// checkAndReindex compares the number of JSON files in .mom/memory/ against
// the SQLite count. If they diverge, triggers a reindex for this scope.
func (a *IndexedAdapter) checkAndReindex() {
	if a.idx == nil {
		return
	}

	memDir := filepath.Join(a.momDir, "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		return
	}

	var jsonCount int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonCount++
		}
	}

	dbCount, err := a.idx.CountByScope(a.scopePath)
	if err != nil {
		// DB error — silently degrade.
		return
	}

	if jsonCount != dbCount {
		// Count diverged (external change) — rebuild index for this scope.
		docs, err := a.json.Query(QueryFilter{})
		if err != nil {
			return
		}
		if err := a.idx.ReindexScope(a.scopePath, docs); err != nil {
			fmt.Fprintf(os.Stderr, "[mom] reindex failed: %v\n", err)
		}
	}
}

// Close releases the underlying SQLite connection.
func (a *IndexedAdapter) Close() error {
	if a.idx != nil {
		return a.idx.Close()
	}
	return nil
}

// Read retrieves a document by ID from JSON (source of truth).
func (a *IndexedAdapter) Read(id string) (*Doc, error) {
	return a.json.Read(id)
}

// Write saves a document to JSON and syncs it to the SQLite index.
func (a *IndexedAdapter) Write(doc *Doc) error {
	if err := a.json.Write(doc); err != nil {
		return err
	}
	if a.idx != nil {
		if err := a.idx.Upsert(doc, a.scopePath); err != nil {
			// Non-fatal: JSON write succeeded; log and continue.
			fmt.Fprintf(os.Stderr, "[mom] index sync failed for %q: %v\n", doc.ID, err)
		}
	}
	return nil
}

// Delete removes a document from JSON and from the SQLite index.
func (a *IndexedAdapter) Delete(id string) error {
	if err := a.json.Delete(id); err != nil {
		return err
	}
	if a.idx != nil {
		if err := a.idx.Delete(id); err != nil {
			fmt.Fprintf(os.Stderr, "[mom] index delete failed for %q: %v\n", id, err)
		}
	}
	return nil
}

// BulkWrite saves multiple documents to JSON and syncs them to the SQLite index.
func (a *IndexedAdapter) BulkWrite(docs []*Doc) error {
	if err := a.json.BulkWrite(docs); err != nil {
		return err
	}
	if a.idx != nil {
		if err := a.idx.BulkUpsert(docs, a.scopePath); err != nil {
			fmt.Fprintf(os.Stderr, "[mom] index bulk sync failed: %v\n", err)
		}
	}
	return nil
}

// Query returns documents matching the filter. Uses JSONAdapter (source of truth).
// Callers who need FTS search should use Search() instead.
func (a *IndexedAdapter) Query(filter QueryFilter) ([]*Doc, error) {
	return a.json.Query(filter)
}

// List returns an in-memory index by scanning JSON files. Uses JSONAdapter.
func (a *IndexedAdapter) List() (*Index, error) {
	return a.json.List()
}

// Search performs FTS5 search (if index available) or falls back to JSONAdapter scan.
// This is the preferred search entry point — all CLI and MCP search paths should use it.
func (a *IndexedAdapter) Search(opts SearchOptions) ([]SearchResult, error) {
	if a.idx != nil {
		return a.idx.Search(opts)
	}
	// Fallback: build results from JSONAdapter scan.
	return a.jsonFallbackSearch(opts)
}

// ListLandmarks returns landmark documents via the SQLite index (fast path) or
// falls back to a JSONAdapter scan.
func (a *IndexedAdapter) ListLandmarks(scopePaths []string, limit int) ([]SearchResult, error) {
	if a.idx != nil {
		return a.idx.ListLandmarks(scopePaths, limit)
	}
	return a.jsonFallbackLandmarks(scopePaths, limit)
}

// Reindex drops and rebuilds the SQLite index from JSON files.
// Called internally on schema/version or cache-count mismatch.
func (a *IndexedAdapter) Reindex() error {
	if a.idx == nil {
		return fmt.Errorf("SQLite index not available")
	}
	docs, err := a.json.Query(QueryFilter{})
	if err != nil {
		return fmt.Errorf("loading docs: %w", err)
	}
	return a.idx.ReindexScope(a.scopePath, docs)
}

// --- Fallback implementations ---

// jsonFallbackSearch is used when SQLite is unavailable.
// It scans all JSON docs and applies simple substring/tag matching.
func (a *IndexedAdapter) jsonFallbackSearch(opts SearchOptions) ([]SearchResult, error) {
	docs, err := a.json.Query(QueryFilter{})
	if err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit == 0 {
		limit = 5
	}

	var results []SearchResult
	for _, doc := range docs {
		if opts.ExcludeDrafts && doc.PromotionState == "draft" {
			continue
		}
		if opts.OnlyLandmarks && !doc.Landmark {
			continue
		}
		if opts.SessionID != "" && doc.SessionID != opts.SessionID {
			continue
		}

		// Tag filter.
		if len(opts.Tags) > 0 {
			docTagSet := make(map[string]bool, len(doc.Tags))
			for _, t := range doc.Tags {
				docTagSet[t] = true
			}
			match := true
			for _, t := range opts.Tags {
				if !docTagSet[t] {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		score := fallbackScore(doc, opts.Query)
		if opts.Query != "" && score <= 0 {
			continue
		}
		if opts.Query == "" {
			score = 1.0
		}

		results = append(results, SearchResult{
			ID:             doc.ID,
			Summary:        buildSummary(doc),
			Tags:           doc.Tags,
			Score:          score,
			ScopePath:      a.scopePath,
			PromotionState: doc.PromotionState,
			Landmark:       doc.Landmark,
			CentralityScore: doc.CentralityScore,
			Created:        doc.Created.UTC().Format("2006-01-02T15:04:05Z"),
			SessionID:      doc.SessionID,
		})
	}

	// Sort by score desc.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// fallbackScore computes a simple score for fallback mode.
func fallbackScore(doc *Doc, query string) float64 {
	if query == "" {
		return 1.0
	}
	q := strings.ToLower(query)
	var score float64

	for _, tag := range doc.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			score += 1.0
		}
	}

	summary := buildSummary(doc)
	if strings.Contains(strings.ToLower(summary), q) {
		score += 1.5
	}

	for _, v := range doc.Content {
		if s, ok := v.(string); ok {
			if strings.Contains(strings.ToLower(s), q) {
				score += 0.5
				break
			}
		}
	}

	if doc.Landmark {
		score += 0.3
	}

	return score
}

// jsonFallbackLandmarks scans JSON docs for landmarks.
func (a *IndexedAdapter) jsonFallbackLandmarks(scopePaths []string, limit int) ([]SearchResult, error) {
	docs, err := a.json.Query(QueryFilter{})
	if err != nil {
		return nil, err
	}

	if limit == 0 {
		limit = 20
	}

	var results []SearchResult
	for _, doc := range docs {
		if !doc.Landmark {
			continue
		}
		score := 0.0
		if doc.CentralityScore != nil {
			score = *doc.CentralityScore
		}
		results = append(results, SearchResult{
			ID:              doc.ID,
			Summary:         buildSummary(doc),
			Tags:            doc.Tags,
			Score:           score,
			ScopePath:       a.scopePath,
			PromotionState:  doc.PromotionState,
			Landmark:        true,
			CentralityScore: doc.CentralityScore,
			Created:         doc.Created.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}

	// Sort by centrality_score desc.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}
