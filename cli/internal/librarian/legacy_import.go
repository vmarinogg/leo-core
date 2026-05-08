package librarian

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// LegacyImportMemory is one legacy JSON memory prepared for central import.
type LegacyImportMemory struct {
	OldID     string
	Memory    InsertMemory
	Tags      []string
	CreatedBy string
	Hash      string
}

// LegacyImportMapping records one old-to-new memory ID mapping.
type LegacyImportMapping struct {
	SourcePath  string `json:"source"`
	OldID       string `json:"old_id"`
	NewID       string `json:"new_id"`
	ContentHash string `json:"content_hash"`
}

// ImportLegacyMemories imports all records for one legacy source in one
// transaction. If the source was already imported with the same fingerprint,
// it returns skipped=true and performs no writes.
func (l *Librarian) ImportLegacyMemories(sourcePath, fingerprint string, records []LegacyImportMemory) ([]LegacyImportMapping, bool, error) {
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(fingerprint) == "" {
		return nil, false, fmt.Errorf("ImportLegacyMemories: %w", ErrEmptyArg)
	}
	mappings := make([]LegacyImportMapping, 0, len(records))
	var skipped bool

	err := l.v.Tx(func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRow(`SELECT source_fingerprint FROM legacy_imports WHERE source_path = ?`, sourcePath).Scan(&existing)
		if err == nil {
			if existing == fingerprint {
				skipped = true
				return nil
			}
			return fmt.Errorf("source already imported with different fingerprint: %s", sourcePath)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		for i := range records {
			r := records[i]
			id, err := l.validateInsert(&r.Memory)
			if err != nil {
				return fmt.Errorf("record %s: %w", r.OldID, err)
			}
			if strings.TrimSpace(r.OldID) == "" {
				r.OldID = id
			}
			if r.Hash == "" {
				sum := sha256.Sum256([]byte(r.Memory.Content))
				r.Hash = fmt.Sprintf("%x", sum[:])
			}
			if err := l.insertMemoryRow(tx, id, r.Memory); err != nil {
				return fmt.Errorf("insert memory %s: %w", r.OldID, err)
			}
			for _, name := range r.Tags {
				if strings.TrimSpace(name) == "" {
					continue
				}
				tagID, err := l.upsertTagInTx(tx, name)
				if err != nil {
					return fmt.Errorf("upsert tag %q: %w", name, err)
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_tags (memory_id, tag_id, created_at) VALUES (?, ?, ?)`, id, tagID, formatTime(l.now())); err != nil {
					return fmt.Errorf("link tag %q: %w", name, err)
				}
			}
			if strings.TrimSpace(r.CreatedBy) != "" {
				entityID, err := l.upsertEntityInTx(tx, "user", r.CreatedBy)
				if err != nil {
					return fmt.Errorf("upsert created_by entity: %w", err)
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO memory_entities (memory_id, entity_id, relationship, created_at) VALUES (?, ?, ?, ?)`, id, entityID, "created_by", formatTime(l.now())); err != nil {
					return fmt.Errorf("link created_by entity: %w", err)
				}
			}
			mappings = append(mappings, LegacyImportMapping{SourcePath: sourcePath, OldID: r.OldID, NewID: id, ContentHash: r.Hash})
		}

		_, err = tx.Exec(`INSERT INTO legacy_imports (source_path, source_fingerprint, imported_at, memory_count, mapping_count) VALUES (?, ?, ?, ?, ?)`, sourcePath, fingerprint, formatTime(l.now()), len(records), len(mappings))
		if err != nil {
			return err
		}
		for _, m := range mappings {
			if _, err := tx.Exec(`INSERT INTO legacy_import_items (source_path, old_id, new_id, content_hash, created_at) VALUES (?, ?, ?, ?, ?)`, m.SourcePath, m.OldID, m.NewID, m.ContentHash, formatTime(l.now())); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("ImportLegacyMemories: %w", err)
	}
	return mappings, skipped, nil
}

func (l *Librarian) upsertEntityInTx(tx *sql.Tx, entityType, displayName string) (string, error) {
	newID := uuid.NewString()
	if _, err := tx.Exec(`INSERT INTO entities (id, type, display_name, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(type, display_name) DO NOTHING`, newID, entityType, displayName, formatTime(l.now())); err != nil {
		return "", err
	}
	var id string
	if err := tx.QueryRow(`SELECT id FROM entities WHERE type = ? AND display_name = ?`, entityType, displayName).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}
