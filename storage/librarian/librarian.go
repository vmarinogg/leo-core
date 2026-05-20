// Package librarian is the universal CRUD layer over the Vault. It is
// the SOLE component that touches Vault — Drafter, Logbook,
// Cartographer, Finder, MCP, Upgrade, and Lens all go through this
// package. Librarian folds together what previous attempts split
// across MemoryStore (memory CRUD) and GraphStore (tags, entities,
// edges) into one module so a memory and its graph context are always
// written and read through the same boundary.
//
// Substance-immutability (ADR 0011) is enforced at the API: substance
// fields are write-once at Insert, operational fields are mutable via
// UpdateOperational. Schema-level constraints (NOT NULL session_id,
// CHECK json_valid(content), UNIQUE(type, display_name) on entities)
// back the API checks as defense in depth.
package librarian

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/momhq/mom/storage/vault"
)

// Librarian wraps an open Vault and exposes the v0.30 CRUD surface.
type Librarian struct {
	v   *vault.Vault
	now func() time.Time
}

// New returns a Librarian backed by the given vault. The caller must
// have opened the vault with Migrations() included so the schema is
// current.
func New(v *vault.Vault) *Librarian {
	return &Librarian{
		v:   v,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// timestampFormat is the fixed-width nanosecond format used for every
// stored timestamp in Librarian-owned tables. RFC3339Nano trims
// trailing zeros and breaks lexical sort across mixed-precision values
// ("...:05Z" sorts before "...:05.5Z" because "." < "Z" in ASCII).
// Always emitting 9 fractional digits keeps the strings sortable.
const timestampFormat = "2006-01-02T15:04:05.000000000Z07:00"

// formatTime renders a UTC time in the canonical Librarian format.
func formatTime(t time.Time) string { return t.UTC().Format(timestampFormat) }

// parseTime parses a stored timestamp using Librarian's canonical
// fixed-width format. v0.30 has never written any other format on
// these tables, so there's no RFC3339Nano fallback to defend against.
// If a future import path ingests legacy rows, this is the place to
// add format tolerance — narrowly scoped to the import boundary, not
// the read path.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timestampFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t, nil
}

// ── memory CRUD ───────────────────────────────────────────────────────────────

// Memory is the read shape returned by Get and the cross-package
// projection of a row in the memories table.
type Memory struct {
	ID                     string
	Type                   string
	Summary                string
	Content                string
	CreatedAt              time.Time
	SessionID              string
	ProjectId              string // ADR 0016 — declared project identity ("" when unknown)
	ProvenanceActor        string
	ProvenanceSourceType   string
	ProvenanceTriggerEvent string
	PromotionState         string
	Landmark               bool
	CentralityScore        sql.NullFloat64
}

// InsertMemory is the write shape for Insert. Substance fields
// (Content, SessionID, Provenance*) are write-once and copied verbatim.
// ID is minted by Librarian per ADR 0013. CreatedAt defaults to now.
// Type defaults to "untyped" per ADR 0012.
type InsertMemory struct {
	Type                   string
	Summary                string
	Content                string
	CreatedAt              time.Time
	SessionID              string
	ProjectId              string // ADR 0016 — declared project identity ("" when unknown)
	ProvenanceActor        string
	ProvenanceSourceType   string
	ProvenanceTriggerEvent string
}

// Insert appends a memory row. It mints a UUID v4 (ADR 0013), defaults
// Type to "untyped" (ADR 0012), and validates required fields before
// any DB write. Returns the minted ID.
//
// Substance fields rejected if empty or invalid: Content (must be
// non-empty valid JSON), SessionID (must be non-empty). Schema-level
// CHECK(json_valid(content)) and NOT NULL session_id back the
// API-layer guard as defense in depth.
func (l *Librarian) Insert(m InsertMemory) (string, error) {
	id, err := l.validateInsert(&m)
	if err != nil {
		return "", err
	}
	if err := l.v.Tx(func(tx *sql.Tx) error {
		return l.insertMemoryRow(tx, id, m)
	}); err != nil {
		return "", fmt.Errorf("Insert: %w", err)
	}
	return id, nil
}

// InsertMemoryWithTags appends a memory row AND links it to every
// named tag in one transaction. Any failure (memory insert, tag
// upsert, edge link) rolls the whole operation back — there is no
// orphan-memory state.
//
// Tags are upserted atomically with INSERT … ON CONFLICT(name) DO
// NOTHING; concurrent calls for the same tag name see the same id
// regardless of which one inserted. Empty/whitespace tag names are
// rejected with ErrEmptyArg before any DB I/O. Callers should
// normalise tag names via NormalizeTagName before calling.
//
// Returns the minted memory ID. On error, no rows have been written.
func (l *Librarian) InsertMemoryWithTags(m InsertMemory, tags []string) (string, error) {
	id, err := l.validateInsert(&m)
	if err != nil {
		return "", err
	}
	for i, name := range tags {
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("InsertMemoryWithTags: tags[%d]: %w", i, ErrEmptyArg)
		}
	}

	if err := l.v.Tx(func(tx *sql.Tx) error {
		if err := l.insertMemoryRow(tx, id, m); err != nil {
			return err
		}
		for _, name := range tags {
			tagID, err := l.upsertTagInTx(tx, name)
			if err != nil {
				return fmt.Errorf("upsert tag %q: %w", name, err)
			}
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO memory_tags (memory_id, tag_id, created_at)
				 VALUES (?, ?, ?)`,
				id, tagID, formatTime(l.now()),
			); err != nil {
				return fmt.Errorf("link tag %q: %w", name, err)
			}
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("InsertMemoryWithTags: %w", err)
	}
	return id, nil
}

// validateInsert mints the memory ID and applies the v0.30 input
// rules (non-empty session_id, non-empty content). Pure — no DB I/O.
// Defaults Type to "untyped" and CreatedAt to now() in place on the
// passed pointer so callers see the resolved values when they
// inspect after the call.
func (l *Librarian) validateInsert(m *InsertMemory) (string, error) {
	if strings.TrimSpace(m.SessionID) == "" {
		return "", fmt.Errorf("validate: session_id: %w", ErrEmptyArg)
	}
	if strings.TrimSpace(m.Content) == "" {
		return "", fmt.Errorf("validate: content: %w", ErrEmptyArg)
	}
	if m.Type == "" {
		m.Type = "untyped"
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = l.now()
	}
	return uuid.NewString(), nil
}

// insertMemoryRow runs the core INSERT INTO memories statement on an
// existing transaction. Used by both Insert and InsertMemoryWithTags
// so the SQL lives in one place.
func (l *Librarian) insertMemoryRow(tx *sql.Tx, id string, m InsertMemory) error {
	_, err := tx.Exec(
		`INSERT INTO memories
		   (id, type, summary, content, created_at, session_id, project_id,
		    provenance_actor, provenance_source_type, provenance_trigger_event)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, m.Type, m.Summary, m.Content, formatTime(m.CreatedAt), m.SessionID,
		nullableStr(m.ProjectId),
		nullableStr(m.ProvenanceActor),
		nullableStr(m.ProvenanceSourceType),
		nullableStr(m.ProvenanceTriggerEvent),
	)
	return err
}

// upsertTagInTx runs the same atomic upsert as UpsertTag but inside
// an existing transaction. Tag names must be non-empty (caller's
// responsibility — InsertMemoryWithTags validates upfront).
func (l *Librarian) upsertTagInTx(tx *sql.Tx, name string) (string, error) {
	newID := uuid.NewString()
	if _, err := tx.Exec(
		`INSERT INTO tags (id, name, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO NOTHING`,
		newID, name, formatTime(l.now()),
	); err != nil {
		return "", err
	}
	var id string
	if err := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}

// Get returns the memory with the given id. Returns ErrNotFound only
// when the query completed cleanly and produced no rows; a mid-
// iteration scan or rows.Err is surfaced verbatim so callers can
// distinguish corruption from a genuine miss.
func (l *Librarian) Get(id string) (Memory, error) {
	var m Memory
	var found bool
	var scanErr error

	err := l.v.Query(
		`SELECT id, type, summary, content, created_at, session_id, project_id,
		        provenance_actor, provenance_source_type, provenance_trigger_event,
		        promotion_state, landmark, centrality_score
		 FROM memories WHERE id = ?`,
		[]any{id},
		func(rs *sql.Rows) error {
			if !rs.Next() {
				return nil
			}
			found = true
			var (
				summary, projectId, actor, sourceType, triggerEvent sql.NullString
				createdAtStr                                        string
				landmarkInt                                         int64
			)
			if err := rs.Scan(
				&m.ID, &m.Type, &summary, &m.Content, &createdAtStr, &m.SessionID,
				&projectId, &actor, &sourceType, &triggerEvent,
				&m.PromotionState, &landmarkInt, &m.CentralityScore,
			); err != nil {
				scanErr = err
				return err
			}
			m.Summary = summary.String
			m.ProjectId = projectId.String
			m.ProvenanceActor = actor.String
			m.ProvenanceSourceType = sourceType.String
			m.ProvenanceTriggerEvent = triggerEvent.String
			m.Landmark = landmarkInt != 0
			t, err := parseTime(createdAtStr)
			if err != nil {
				scanErr = err
				return err
			}
			m.CreatedAt = t
			return nil
		},
	)
	// Order matters: a scan-time error must surface even if the query
	// returned rows, otherwise corruption is silently translated to
	// ErrNotFound.
	if scanErr != nil {
		return Memory{}, fmt.Errorf("Get %s: scan: %w", id, scanErr)
	}
	if err != nil {
		return Memory{}, fmt.Errorf("Get %s: %w", id, err)
	}
	if !found {
		return Memory{}, ErrNotFound
	}
	return m, nil
}

// OperationalUpdate is the patch shape for UpdateOperational. Each
// field is optional; a non-nil pointer marks a field for update. Any
// substance field passed via this surface is rejected with
// ErrSubstanceImmutable — callers cannot rewrite content, provenance,
// or session attribution after Insert.
type OperationalUpdate struct {
	Type            *string
	PromotionState  *string
	Landmark        *bool
	CentralityScore *float64
}

// CurateDraft turns a draft into a curated memory in one update.
func (l *Librarian) CurateDraft(id, memoryType, summary string) error {
	id = strings.TrimSpace(id)
	memoryType = strings.TrimSpace(memoryType)
	summary = strings.TrimSpace(summary)
	if id == "" {
		return fmt.Errorf("CurateDraft: id: %w", ErrEmptyArg)
	}
	if memoryType == "" {
		return fmt.Errorf("CurateDraft: type: %w", ErrEmptyArg)
	}
	if summary == "" {
		return fmt.Errorf("CurateDraft: summary: %w", ErrEmptyArg)
	}
	if memoryType != "semantic" && memoryType != "procedural" && memoryType != "episodic" {
		return fmt.Errorf("CurateDraft: invalid type %q", memoryType)
	}
	return l.v.Tx(func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRow(`SELECT promotion_state FROM memories WHERE id = ?`, id).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if state == "curated" {
			return fmt.Errorf("already curated")
		}
		res, err := tx.Exec(
			`UPDATE memories SET type = ?, summary = ?, promotion_state = 'curated' WHERE id = ?`,
			memoryType, summary, id,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// Delete removes a memory by id. ON DELETE CASCADE on memory_tags
// and memory_entities (per migration 2) cleans up edges
// automatically; tag and entity rows themselves are untouched.
//
// Returns ErrNotFound if no memory matches. Substance is gone after
// this call — there is no recovery.
func (l *Librarian) Delete(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("Delete: id: %w", ErrEmptyArg)
	}
	return l.v.Tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM memories WHERE id = ?`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// UpdateOperational mutates only the operational columns of a memory.
// Substance columns are unreachable through this API. Empty patches
// are a no-op success.
func (l *Librarian) UpdateOperational(id string, patch OperationalUpdate) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("UpdateOperational: id: %w", ErrEmptyArg)
	}

	var (
		setParts []string
		args     []any
	)
	if patch.Type != nil {
		setParts = append(setParts, "type = ?")
		args = append(args, *patch.Type)
	}
	if patch.PromotionState != nil {
		setParts = append(setParts, "promotion_state = ?")
		args = append(args, *patch.PromotionState)
	}
	if patch.Landmark != nil {
		v := 0
		if *patch.Landmark {
			v = 1
		}
		setParts = append(setParts, "landmark = ?")
		args = append(args, v)
	}
	if patch.CentralityScore != nil {
		setParts = append(setParts, "centrality_score = ?")
		args = append(args, *patch.CentralityScore)
	}
	if len(setParts) == 0 {
		return nil
	}

	args = append(args, id)
	stmt := "UPDATE memories SET " + strings.Join(setParts, ", ") + " WHERE id = ?"

	return l.v.Tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(stmt, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func nullableStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
