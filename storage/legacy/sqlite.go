// Package storage — sqliteIndex provides a SQLite+FTS5 search index for
// memory documents. JSON files remain the source of truth; this index is
// a rebuildable cache stored in .mom/cache/index.db.
package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	// Pure-Go SQLite driver — no CGO required.
	_ "modernc.org/sqlite"
)

const (
	// dbSchemaVersion is bumped whenever the schema changes.
	// A version mismatch triggers a full reindex.
	dbSchemaVersion = "3"

	// dbFileName is the SQLite database file inside .mom/cache/.
	dbFileName = "index.db"

	// pragmas applied once after opening the DB.
	pragmaWAL         = "PRAGMA journal_mode=WAL"
	pragmaForeignKeys = "PRAGMA foreign_keys=ON"
	pragmaSynchronous = "PRAGMA synchronous=NORMAL"
	pragmaCacheSize   = "PRAGMA cache_size=-8000" // 8 MB page cache
)

// sqliteIndex wraps a SQLite database providing FTS5-backed search for
// memory documents. All methods are safe for sequential use; concurrent
// access is serialised by SQLite's WAL reader/writer locking.
type sqliteIndex struct {
	db     *sql.DB
	dbPath string
}

// openSQLiteIndex opens (or creates) the SQLite index at
// <momDir>/cache/index.db. Returns an error if the database cannot be
// opened or if the schema cannot be created/validated.
func openSQLiteIndex(momDir string) (*sqliteIndex, error) {
	cacheDir := filepath.Join(momDir, "cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}

	dbPath := filepath.Join(cacheDir, dbFileName)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite db: %w", err)
	}

	// Apply connection pragmas.
	for _, pragma := range []string{pragmaWAL, pragmaForeignKeys, pragmaSynchronous, pragmaCacheSize} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("applying pragma %q: %w", pragma, err)
		}
	}

	idx := &sqliteIndex{db: db, dbPath: dbPath}
	if err := idx.createSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return idx, nil
}

// Close releases the database connection.
func (idx *sqliteIndex) Close() error {
	if idx.db != nil {
		return idx.db.Close()
	}
	return nil
}

// createSchema creates the tables and FTS5 virtual table if they don't
// exist, and validates the stored schema version.
func (idx *sqliteIndex) createSchema() error {
	stmts := []string{
		// Main metadata table.
		`CREATE TABLE IF NOT EXISTS memories (
			id                TEXT PRIMARY KEY,
			scope             TEXT NOT NULL,
			scope_path        TEXT NOT NULL,
			summary           TEXT,
			tags              TEXT,
			tags_json         TEXT,
			classification    TEXT DEFAULT 'INTERNAL',
			promotion_state   TEXT DEFAULT 'draft',
			landmark          INTEGER DEFAULT 0,
			centrality_score  REAL,
			session_id        TEXT,
			created           TEXT,
			created_by        TEXT,
			updated           TEXT,
			content_text      TEXT
		)`,
		// FTS5 virtual table backed by the memories table.
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
			id,
			summary,
			tags,
			content_text,
			content=memories,
			content_rowid=rowid,
			tokenize='porter unicode61'
		)`,
		// Triggers to keep FTS5 in sync.
		`CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
			INSERT INTO memories_fts(rowid, id, summary, tags, content_text)
			VALUES (new.rowid, new.id, new.summary, new.tags, new.content_text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, id, summary, tags, content_text)
			VALUES ('delete', old.rowid, old.id, old.summary, old.tags, old.content_text);
		END`,
		`CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
			INSERT INTO memories_fts(memories_fts, rowid, id, summary, tags, content_text)
			VALUES ('delete', old.rowid, old.id, old.summary, old.tags, old.content_text);
			INSERT INTO memories_fts(rowid, id, summary, tags, content_text)
			VALUES (new.rowid, new.id, new.summary, new.tags, new.content_text);
		END`,
		// Index metadata.
		`CREATE TABLE IF NOT EXISTS index_meta (
			key   TEXT PRIMARY KEY,
			value TEXT
		)`,
	}

	for _, stmt := range stmts {
		if _, err := idx.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(len(stmt), 60)], err)
		}
	}

	// Check schema version; if different, wipe and rebuild on first insert.
	var storedVersion string
	row := idx.db.QueryRow(`SELECT value FROM index_meta WHERE key = 'schema_version'`)
	if err := row.Scan(&storedVersion); err != nil {
		// Version not stored yet — write it.
		_, err = idx.db.Exec(`INSERT OR REPLACE INTO index_meta (key,value) VALUES ('schema_version',?)`, dbSchemaVersion)
		return err
	}

	if storedVersion != dbSchemaVersion {
		// Schema changed — wipe all indexed data. Caller will reindex.
		if err := idx.wipeAll(); err != nil {
			return fmt.Errorf("wiping stale schema: %w", err)
		}
		_, err := idx.db.Exec(`INSERT OR REPLACE INTO index_meta (key,value) VALUES ('schema_version',?)`, dbSchemaVersion)
		return err
	}

	return nil
}

// wipeAll removes all rows from memories and rebuilds the FTS shadow tables.
func (idx *sqliteIndex) wipeAll() error {
	stmts := []string{
		`DELETE FROM memories`,
		`INSERT INTO memories_fts(memories_fts) VALUES ('rebuild')`,
	}
	for _, s := range stmts {
		if _, err := idx.db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// Upsert inserts or replaces a document in the index.
func (idx *sqliteIndex) Upsert(doc *Doc, scopePath string) error {
	tagsJSON, _ := json.Marshal(doc.Tags)
	_, err := idx.db.Exec(`
		INSERT OR REPLACE INTO memories
			(id, scope, scope_path, summary, tags, tags_json, classification,
			 promotion_state, landmark, centrality_score, session_id, created,
			 created_by, content_text)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		doc.ID, doc.Scope, scopePath,
		buildSummary(doc),
		strings.Join(doc.Tags, " "),
		string(tagsJSON),
		doc.Classification,
		doc.PromotionState,
		boolToInt(doc.Landmark),
		doc.CentralityScore,
		doc.SessionID,
		doc.Created.UTC().Format("2006-01-02T15:04:05Z"),
		doc.CreatedBy,
		buildContentText(doc),
	)
	if err != nil {
		return fmt.Errorf("upserting %q: %w", doc.ID, err)
	}
	return nil
}

// Delete removes a document from the index by ID.
func (idx *sqliteIndex) Delete(id string) error {
	_, err := idx.db.Exec(`DELETE FROM memories WHERE id = ?`, id)
	return err
}

// buildSummary extracts a summary string from a Doc.
// Mirrors logic used across MCP tools and CLI recall.
func buildSummary(doc *Doc) string {
	if s, ok := doc.Content["summary"].(string); ok {
		return s
	}
	return ""
}

// buildContentText flattens all string content values into a single search text.
func buildContentText(doc *Doc) string {
	var parts []string
	for _, v := range doc.Content {
		switch s := v.(type) {
		case string:
			parts = append(parts, s)
		case []any:
			for _, item := range s {
				if str, ok := item.(string); ok {
					parts = append(parts, str)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SearchResult is a result from the SQLite FTS5 search.
type SearchResult struct {
	ID              string
	Summary         string
	Tags            []string
	Score           float64 // BM25 score from FTS5 (negative → higher is better after negation)
	ScopePath       string
	PromotionState  string
	Landmark        bool
	CentralityScore *float64
	Created         string
	SessionID       string
}

// QueryType controls FTS5 token matching behaviour.
type QueryType int

const (
	// QueryOR uses implicit OR — any token match scores (default, broader).
	QueryOR QueryType = iota
	// QueryAND requires all tokens to match (stricter, higher precision).
	QueryAND
)

// SearchOptions controls a Search call.
type SearchOptions struct {
	// Query is a free-text search string. Empty = return all (no FTS filter).
	Query string
	// QueryType selects AND (all tokens required) or OR (any token scores).
	// Defaults to QueryOR when zero.
	QueryType QueryType
	// ScopePaths restricts results to specific .mom/ directories.
	// Empty = all scopes.
	ScopePaths []string
	// Tags are ANDed; all must match.
	Tags []string
	// ExcludeDrafts excludes docs with promotion_state = 'draft'.
	ExcludeDrafts bool
	// OnlyLandmarks restricts to landmark=1 docs.
	OnlyLandmarks bool
	// SessionID restricts to a specific session.
	SessionID string
	// Limit caps the result count (0 = no cap).
	Limit int
}

// Search performs FTS5 search with optional filters.
// Results are ordered by BM25 score (best match first) with a landmark boost.
func (idx *sqliteIndex) Search(opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit == 0 {
		opts.Limit = 5
	}

	var args []any
	var conds []string

	// Scope filter.
	if len(opts.ScopePaths) > 0 {
		placeholders := make([]string, len(opts.ScopePaths))
		for i, sp := range opts.ScopePaths {
			placeholders[i] = "?"
			args = append(args, sp)
		}
		conds = append(conds, "m.scope_path IN ("+strings.Join(placeholders, ",")+")")
	}

	// Promotion state filter (#147 — exclude drafts by default).
	if opts.ExcludeDrafts {
		conds = append(conds, "m.promotion_state != 'draft'")
	}

	// Landmarks only.
	if opts.OnlyLandmarks {
		conds = append(conds, "m.landmark = 1")
	}

	// Session filter.
	if opts.SessionID != "" {
		conds = append(conds, "m.session_id = ?")
		args = append(args, opts.SessionID)
	}

	// Tag filter (AND logic — each tag must appear in tags_json).
	for _, tag := range opts.Tags {
		conds = append(conds, "m.tags_json LIKE ?")
		args = append(args, "%\""+tag+"\"%")
	}

	var query string
	if opts.Query == "" {
		// No text query — list all matching docs.
		wherePart := ""
		if len(conds) > 0 {
			wherePart = "WHERE " + strings.Join(conds, " AND ")
		}
		query = `
			SELECT m.id, m.summary, m.tags_json, 1.0 AS raw_score,
			       m.scope_path, m.promotion_state, m.landmark,
			       m.centrality_score, m.created, m.session_id
			FROM memories m
			` + wherePart + `
			ORDER BY m.landmark DESC, m.centrality_score DESC, m.created DESC
			LIMIT ?`
		args = append(args, opts.Limit)
	} else {
		// FTS5 MATCH query — bm25() returns negative values (more negative = better).
		// Column weights: id=0, summary=2, tags=1, content_text=10 (ADR 0007).
		ftsConds := "memories_fts MATCH ?"
		var ftsArg string
		if opts.QueryType == QueryAND {
			ftsArg = buildFTSQueryAND(opts.Query)
		} else {
			ftsArg = buildFTSQueryOR(opts.Query)
		}

		// Build the WHERE clause combining FTS join and other filters.
		allConds := append([]string{ftsConds}, conds...)
		wherePart := "WHERE " + strings.Join(allConds, " AND ")

		query = `
			SELECT m.id, m.summary, m.tags_json, -bm25(memories_fts, 0, 2, 1, 10) AS raw_score,
			       m.scope_path, m.promotion_state, m.landmark,
			       m.centrality_score, m.created, m.session_id
			FROM memories_fts
			JOIN memories m ON m.rowid = memories_fts.rowid
			` + wherePart + `
			ORDER BY raw_score DESC, m.landmark DESC
			LIMIT ?`
		args = append([]any{ftsArg}, args...)
		args = append(args, opts.Limit)
	}

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var tagsJSON string
		var landmarkInt int
		var centralityScore sql.NullFloat64
		var sessionID sql.NullString
		var created sql.NullString

		if err := rows.Scan(
			&r.ID, &r.Summary, &tagsJSON, &r.Score,
			&r.ScopePath, &r.PromotionState, &landmarkInt,
			&centralityScore, &created, &sessionID,
		); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		r.Landmark = landmarkInt == 1
		if centralityScore.Valid {
			v := centralityScore.Float64
			r.CentralityScore = &v
		}
		if sessionID.Valid {
			r.SessionID = sessionID.String
		}
		if created.Valid {
			r.Created = created.String
		}

		// Parse tags from JSON.
		if tagsJSON != "" {
			json.Unmarshal([]byte(tagsJSON), &r.Tags) //nolint:errcheck
		}

		// Apply landmark boost (mirrors existing scoring logic).
		if r.Landmark {
			r.Score += 0.3
		}

		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	return results, nil
}

// buildFTSQueryOR builds an FTS5 MATCH expression with implicit OR —
// any token match scores (broader, higher recall).
func buildFTSQueryOR(query string) string {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.ReplaceAll(t, `"`, `""`)
		quoted = append(quoted, `"`+t+`"`)
	}
	return strings.Join(quoted, " ")
}

// buildFTSQueryAND builds an FTS5 MATCH expression requiring all tokens —
// every token must appear in the document (stricter, higher precision).
func buildFTSQueryAND(query string) string {
	tokens := strings.Fields(strings.ToLower(query))
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.ReplaceAll(t, `"`, `""`)
		quoted = append(quoted, `+"`+t+`"`)
	}
	return strings.Join(quoted, " ")
}

// CountByScope returns the number of indexed documents for a given scope path.
func (idx *sqliteIndex) CountByScope(scopePath string) (int, error) {
	var count int
	err := idx.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE scope_path = ?`, scopePath).Scan(&count)
	return count, err
}

// BulkUpsert inserts or replaces multiple documents in a single transaction.
func (idx *sqliteIndex) BulkUpsert(docs []*Doc, scopePath string) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO memories
			(id, scope, scope_path, summary, tags, tags_json, classification,
			 promotion_state, landmark, centrality_score, session_id, created,
			 created_by, content_text)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, doc := range docs {
		tagsJSON, _ := json.Marshal(doc.Tags)
		if _, err := stmt.Exec(
			doc.ID, doc.Scope, scopePath,
			buildSummary(doc),
			strings.Join(doc.Tags, " "),
			string(tagsJSON),
			doc.Classification,
			doc.PromotionState,
			boolToInt(doc.Landmark),
			doc.CentralityScore,
			doc.SessionID,
			doc.Created.UTC().Format("2006-01-02T15:04:05Z"),
			doc.CreatedBy,
			buildContentText(doc),
		); err != nil {
			return fmt.Errorf("inserting %q: %w", doc.ID, err)
		}
	}

	return tx.Commit()
}

// ReindexScope removes all records for a scope and re-inserts them from the
// provided docs slice. Called when a count divergence is detected at startup.
func (idx *sqliteIndex) ReindexScope(scopePath string, docs []*Doc) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning reindex transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Delete stale records for this scope. Then also delete each incoming ID
	// regardless of scope_path so path canonicalization changes cannot leave an
	// old row with the same primary key under a previous path spelling.
	if _, err := tx.Exec(`DELETE FROM memories WHERE scope_path = ?`, scopePath); err != nil {
		return fmt.Errorf("deleting scope records: %w", err)
	}
	deleteByID, err := tx.Prepare(`DELETE FROM memories WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("preparing stale id delete: %w", err)
	}
	defer deleteByID.Close()
	for _, doc := range docs {
		if _, err := deleteByID.Exec(doc.ID); err != nil {
			return fmt.Errorf("deleting stale row %q: %w", doc.ID, err)
		}
	}

	stmt, err := tx.Prepare(`
		INSERT INTO memories
			(id, scope, scope_path, summary, tags, tags_json, classification,
			 promotion_state, landmark, centrality_score, session_id, created,
			 created_by, content_text)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer stmt.Close()

	for _, doc := range docs {
		tagsJSON, _ := json.Marshal(doc.Tags)
		if _, err := stmt.Exec(
			doc.ID, doc.Scope, scopePath,
			buildSummary(doc),
			strings.Join(doc.Tags, " "),
			string(tagsJSON),
			doc.Classification,
			doc.PromotionState,
			boolToInt(doc.Landmark),
			doc.CentralityScore,
			doc.SessionID,
			doc.Created.UTC().Format("2006-01-02T15:04:05Z"),
			doc.CreatedBy,
			buildContentText(doc),
		); err != nil {
			return fmt.Errorf("inserting %q: %w", doc.ID, err)
		}
	}

	// Rebuild FTS shadow tables.
	if _, err := tx.Exec(`INSERT INTO memories_fts(memories_fts) VALUES ('rebuild')`); err != nil {
		return fmt.Errorf("rebuilding FTS: %w", err)
	}

	return tx.Commit()
}

// ListLandmarks returns landmark documents sorted by centrality_score descending.
func (idx *sqliteIndex) ListLandmarks(scopePaths []string, limit int) ([]SearchResult, error) {
	if limit == 0 {
		limit = 20
	}

	var args []any
	var conds []string

	if len(scopePaths) > 0 {
		placeholders := make([]string, len(scopePaths))
		for i, sp := range scopePaths {
			placeholders[i] = "?"
			args = append(args, sp)
		}
		conds = append(conds, "scope_path IN ("+strings.Join(placeholders, ",")+")")
	}

	conds = append(conds, "landmark = 1")
	where := "WHERE " + strings.Join(conds, " AND ")
	args = append(args, limit)

	query := `SELECT id, summary, tags_json, 0.0, scope_path, promotion_state,
	                 landmark, centrality_score, created, session_id
	          FROM memories ` + where + `
	          ORDER BY centrality_score DESC NULLS LAST
	          LIMIT ?`

	rows, err := idx.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("landmark query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var tagsJSON string
		var landmarkInt int
		var centralityScore sql.NullFloat64
		var sessionID sql.NullString
		var created sql.NullString

		if err := rows.Scan(
			&r.ID, &r.Summary, &tagsJSON, &r.Score,
			&r.ScopePath, &r.PromotionState, &landmarkInt,
			&centralityScore, &created, &sessionID,
		); err != nil {
			return nil, err
		}
		r.Landmark = landmarkInt == 1
		if centralityScore.Valid {
			v := centralityScore.Float64
			r.CentralityScore = &v
		}
		if tagsJSON != "" {
			json.Unmarshal([]byte(tagsJSON), &r.Tags) //nolint:errcheck
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
