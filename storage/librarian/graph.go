package librarian

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ── tags ──────────────────────────────────────────────────────────────────────

// UpsertTag inserts a tag with the given name or returns the id of an
// existing matching row. Names must be non-empty after trimming;
// callers should normalise via NormalizeTagName before calling.
//
// Comparison is case-sensitive at the storage layer; "MCP" and "mcp"
// are different tags. Normalisation is a higher-level convention.
//
// The whole operation is one Tx with INSERT … ON CONFLICT(name) DO
// NOTHING followed by SELECT id WHERE name = ?. Two concurrent calls
// for the same name both succeed and both return the same id; neither
// surfaces a UNIQUE constraint error. Atomicity is guaranteed by the
// transaction, not by lookup-then-insert.
func (l *Librarian) UpsertTag(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("UpsertTag: name: %w", ErrEmptyArg)
	}
	var id string
	err := l.v.Tx(func(tx *sql.Tx) error {
		newID := uuid.NewString()
		if _, err := tx.Exec(
			`INSERT INTO tags (id, name, created_at) VALUES (?, ?, ?)
			 ON CONFLICT(name) DO NOTHING`,
			newID, name, formatTime(l.now()),
		); err != nil {
			return err
		}
		// Always SELECT — INSERT ON CONFLICT DO NOTHING does not
		// reliably return last_insert_rowid for the conflict case,
		// and either the just-inserted row or the pre-existing one
		// is what we want.
		return tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, name).Scan(&id)
	})
	if err != nil {
		return "", fmt.Errorf("UpsertTag: %w", err)
	}
	return id, nil
}

// LinkTag attaches a tag to a memory. Idempotent: a duplicate edge is
// a no-op success.
func (l *Librarian) LinkTag(memoryID, tagID string) error {
	if strings.TrimSpace(memoryID) == "" || strings.TrimSpace(tagID) == "" {
		return fmt.Errorf("LinkTag: %w", ErrEmptyArg)
	}
	return l.v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO memory_tags (memory_id, tag_id, created_at)
			 VALUES (?, ?, ?)`,
			memoryID, tagID, formatTime(l.now()),
		)
		return err
	})
}

// MemoriesByTag returns memory IDs linked to the tag with the given
// name. Returns an empty slice (not nil) and no error when the tag is
// unknown or has no linked memories.
func (l *Librarian) TagsForMemory(memoryID string) ([]string, error) {
	if strings.TrimSpace(memoryID) == "" {
		return nil, fmt.Errorf("TagsForMemory: %w", ErrEmptyArg)
	}
	names := []string{}
	err := l.v.Query(
		`SELECT t.name FROM tags t
		   JOIN memory_tags mt ON mt.tag_id = t.id
		  WHERE mt.memory_id = ?
		  ORDER BY t.name`,
		[]any{memoryID},
		func(rs *sql.Rows) error {
			for rs.Next() {
				var name string
				if err := rs.Scan(&name); err != nil {
					return err
				}
				names = append(names, name)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("TagsForMemory: %w", err)
	}
	return names, nil
}

func (l *Librarian) MemoriesByTag(name string) ([]string, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("MemoriesByTag: %w", ErrEmptyArg)
	}
	ids := []string{}
	err := l.v.Query(
		`SELECT m.id FROM memories m
		   JOIN memory_tags mt ON mt.memory_id = m.id
		   JOIN tags t         ON t.id        = mt.tag_id
		  WHERE t.name = ?
		  ORDER BY m.created_at DESC, m.id DESC`,
		[]any{name},
		func(rs *sql.Rows) error {
			for rs.Next() {
				var id string
				if err := rs.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("MemoriesByTag: %w", err)
	}
	return ids, nil
}

// AllTagNames returns every tag name in the corpus, in lexical order.
// Used by Drafter to seed BM25 ranking when extracting per-chunk
// tags — phrases already saturated as tags get downranked, novel
// phrases get upranked. Returns an empty slice (not nil) when the
// corpus is empty so callers can pass the result straight into
// newBM25Index without nil-handling.
func (l *Librarian) AllTagNames() ([]string, error) {
	names := []string{}
	err := l.v.Query(
		`SELECT name FROM tags ORDER BY name`,
		nil,
		func(rs *sql.Rows) error {
			for rs.Next() {
				var n string
				if err := rs.Scan(&n); err != nil {
					return err
				}
				names = append(names, n)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("AllTagNames: %w", err)
	}
	return names, nil
}

// RenameTag renames a tag in place. The comparison is case-sensitive
// so "mcp" → "MCP" works as expected. Renames mutate the single tags
// row; no memory or edge row is rewritten — locked by ADR 0010.
func (l *Librarian) RenameTag(oldName, newName string) error {
	if strings.TrimSpace(oldName) == "" || strings.TrimSpace(newName) == "" {
		return fmt.Errorf("RenameTag: %w", ErrEmptyArg)
	}
	return l.v.Tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE tags SET name = ? WHERE name = ?`, newName, oldName)
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

// MergeTags re-points every memory_tags edge currently pointing at the
// source tag to the target tag, then deletes the source tag row.
//
// Rejects source == target with ErrSelfMerge — without this guard, a
// self-merge would re-point edges from a tag to itself (no-op) and
// then delete the tag plus all its edges via ON CASCADE-ish cleanup,
// silently wiping a real tag.
//
// Comparison is case-sensitive: "mcp" and "MCP" are distinct.
func (l *Librarian) MergeTags(source, target string) error {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
		return fmt.Errorf("MergeTags: %w", ErrEmptyArg)
	}
	if source == target {
		return ErrSelfMerge
	}
	return l.v.Tx(func(tx *sql.Tx) error {
		var srcID, tgtID string
		row := tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, source)
		if err := row.Scan(&srcID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("MergeTags source %q: %w", source, ErrNotFound)
			}
			return err
		}
		row = tx.QueryRow(`SELECT id FROM tags WHERE name = ?`, target)
		if err := row.Scan(&tgtID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("MergeTags target %q: %w", target, ErrNotFound)
			}
			return err
		}
		// Re-point edges. INSERT OR IGNORE collapses duplicates where a
		// memory was linked to both source and target before the merge.
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO memory_tags (memory_id, tag_id, created_at)
			 SELECT memory_id, ?, created_at FROM memory_tags WHERE tag_id = ?`,
			tgtID, srcID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM memory_tags WHERE tag_id = ?`, srcID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM tags WHERE id = ?`, srcID); err != nil {
			return err
		}
		return nil
	})
}

// ── entities ──────────────────────────────────────────────────────────────────

// UpsertEntity inserts an entity of the given type and display name, or
// returns the id of an existing matching row. Both type and
// display_name must be non-empty after trimming. The schema enforces
// UNIQUE(type, display_name); the API enforces non-empty inputs.
//
// One Tx with INSERT … ON CONFLICT(type, display_name) DO NOTHING
// followed by SELECT id. Concurrent calls for the same (type, name)
// pair are safe — see UpsertTag for the atomicity rationale.
func (l *Librarian) UpsertEntity(entityType, displayName string) (string, error) {
	if strings.TrimSpace(entityType) == "" {
		return "", fmt.Errorf("UpsertEntity: type: %w", ErrEmptyArg)
	}
	if strings.TrimSpace(displayName) == "" {
		return "", fmt.Errorf("UpsertEntity: display_name: %w", ErrEmptyArg)
	}
	var id string
	err := l.v.Tx(func(tx *sql.Tx) error {
		newID := uuid.NewString()
		if _, err := tx.Exec(
			`INSERT INTO entities (id, type, display_name, created_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(type, display_name) DO NOTHING`,
			newID, entityType, displayName, formatTime(l.now()),
		); err != nil {
			return err
		}
		return tx.QueryRow(
			`SELECT id FROM entities WHERE type = ? AND display_name = ?`,
			entityType, displayName,
		).Scan(&id)
	})
	if err != nil {
		return "", fmt.Errorf("UpsertEntity: %w", err)
	}
	return id, nil
}

// LinkEntity attaches an entity to a memory under the given
// relationship label (e.g., "created_by", "mentions"). Idempotent for
// the (memory_id, entity_id, relationship) tuple.
func (l *Librarian) LinkEntity(memoryID, entityID, relationship string) error {
	if strings.TrimSpace(memoryID) == "" ||
		strings.TrimSpace(entityID) == "" ||
		strings.TrimSpace(relationship) == "" {
		return fmt.Errorf("LinkEntity: %w", ErrEmptyArg)
	}
	return l.v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO memory_entities
			   (memory_id, entity_id, relationship, created_at)
			 VALUES (?, ?, ?, ?)`,
			memoryID, entityID, relationship, formatTime(l.now()),
		)
		return err
	})
}

// MemoriesByEntity returns memory IDs linked to the entity identified
// by (type, display_name). Returns empty slice (not nil) for unknown
// or unlinked entities.
func (l *Librarian) MemoriesByEntity(entityType, displayName string) ([]string, error) {
	if strings.TrimSpace(entityType) == "" || strings.TrimSpace(displayName) == "" {
		return nil, fmt.Errorf("MemoriesByEntity: %w", ErrEmptyArg)
	}
	ids := []string{}
	// SELECT DISTINCT — a memory linked to one entity under multiple
	// relationships ("created_by" + "mentions") would otherwise appear
	// once per edge. The contract of MemoriesByEntity is "memories that
	// reference this entity," not "edges to this entity," so dedupe at
	// the SQL level.
	err := l.v.Query(
		`SELECT DISTINCT m.id FROM memories m
		   JOIN memory_entities me ON me.memory_id = m.id
		   JOIN entities e         ON e.id        = me.entity_id
		  WHERE e.type = ? AND e.display_name = ?
		  ORDER BY m.created_at DESC, m.id DESC`,
		[]any{entityType, displayName},
		func(rs *sql.Rows) error {
			for rs.Next() {
				var id string
				if err := rs.Scan(&id); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("MemoriesByEntity: %w", err)
	}
	return ids, nil
}
