package librarian

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// LegacyLogImportEvent is one legacy operational event prepared for central import.
type LegacyLogImportEvent struct {
	SourceItemID string
	Event        OpEvent
	Hash         string
}

// LegacyLogImportMapping records one legacy log item to op_events row mapping.
type LegacyLogImportMapping struct {
	SourcePath   string `json:"source"`
	SourceItemID string `json:"source_item_id"`
	OpEventID    int64  `json:"op_event_id"`
	ContentHash  string `json:"content_hash"`
	EventType    string `json:"event_type"`
	SessionID    string `json:"session_id"`
	CreatedAt    string `json:"created_at"`
}

// ImportLegacyLogEvents imports all records for one legacy log source in one transaction.
func (l *Librarian) ImportLegacyLogEvents(sourcePath, fingerprint string, events []LegacyLogImportEvent) ([]LegacyLogImportMapping, bool, error) {
	if strings.TrimSpace(sourcePath) == "" || strings.TrimSpace(fingerprint) == "" {
		return nil, false, fmt.Errorf("ImportLegacyLogEvents: %w", ErrEmptyArg)
	}
	mappings := make([]LegacyLogImportMapping, 0, len(events))
	var skipped bool

	err := l.v.Tx(func(tx *sql.Tx) error {
		var existing string
		err := tx.QueryRow(`SELECT source_fingerprint FROM legacy_log_imports WHERE source_path = ?`, sourcePath).Scan(&existing)
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

		for i := range events {
			e := events[i]
			if strings.TrimSpace(e.SourceItemID) == "" {
				return fmt.Errorf("event %d: source_item_id: %w", i, ErrEmptyArg)
			}
			if strings.TrimSpace(e.Event.EventType) == "" {
				return fmt.Errorf("event %s: event_type: %w", e.SourceItemID, ErrEmptyArg)
			}
			if strings.TrimSpace(e.Event.SessionID) == "" {
				return fmt.Errorf("event %s: session_id: %w", e.SourceItemID, ErrEmptyArg)
			}
			if e.Event.CreatedAt.IsZero() {
				e.Event.CreatedAt = l.now()
			}
			payload := e.Event.Payload
			if len(payload) == 0 {
				payload = nil
			}
			payloadJSON, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("event %s payload: %w", e.SourceItemID, err)
			}
			if e.Hash == "" {
				sum := sha256.Sum256(payloadJSON)
				e.Hash = fmt.Sprintf("%x", sum[:])
			}
			res, err := tx.Exec(
				`INSERT INTO op_events (event_type, session_id, created_at, payload)
				 VALUES (?, ?, ?, ?)`,
				e.Event.EventType, e.Event.SessionID, formatTime(e.Event.CreatedAt), string(payloadJSON),
			)
			if err != nil {
				return fmt.Errorf("insert event %s: %w", e.SourceItemID, err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			mappings = append(mappings, LegacyLogImportMapping{
				SourcePath:   sourcePath,
				SourceItemID: e.SourceItemID,
				OpEventID:    id,
				ContentHash:  e.Hash,
				EventType:    e.Event.EventType,
				SessionID:    e.Event.SessionID,
				CreatedAt:    formatTime(e.Event.CreatedAt),
			})
		}

		_, err = tx.Exec(
			`INSERT INTO legacy_log_imports
			 (source_path, source_fingerprint, imported_at, event_count, mapping_count)
			 VALUES (?, ?, ?, ?, ?)`,
			sourcePath, fingerprint, formatTime(l.now()), len(events), len(mappings),
		)
		if err != nil {
			return err
		}
		for _, m := range mappings {
			_, err := tx.Exec(
				`INSERT INTO legacy_log_import_items
				 (source_path, source_item_id, op_event_id, content_hash, event_type, session_id, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				m.SourcePath, m.SourceItemID, m.OpEventID, m.ContentHash, m.EventType, m.SessionID, m.CreatedAt,
			)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("ImportLegacyLogEvents: %w", err)
	}
	return mappings, skipped, nil
}
