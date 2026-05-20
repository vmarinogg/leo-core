package librarian

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CrierCheckpoint is the durable cursor Crier (ADR 0022) advances as
// it projects Ledger events into the Vault. -1 means "no events
// applied yet"; the next event to project is offset = checkpoint + 1.
type CrierCheckpoint struct {
	Offset    int64
	UpdatedAt time.Time
}

// GetCrierCheckpoint returns the current checkpoint. Reads the
// single row in crier_state (created by migration 6).
func (l *Librarian) GetCrierCheckpoint() (CrierCheckpoint, error) {
	var c CrierCheckpoint
	var updatedAt string
	var found bool
	err := l.v.Query(
		`SELECT checkpoint, updated_at FROM crier_state WHERE id = 1`,
		nil,
		func(rs *sql.Rows) error {
			if !rs.Next() {
				return nil
			}
			found = true
			return rs.Scan(&c.Offset, &updatedAt)
		},
	)
	if err != nil {
		return CrierCheckpoint{}, fmt.Errorf("GetCrierCheckpoint: %w", err)
	}
	if !found {
		return CrierCheckpoint{}, ErrNoCheckpoint
	}
	if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
		c.UpdatedAt = t
	} else if t2, err := time.Parse("2006-01-02 15:04:05", updatedAt); err == nil {
		c.UpdatedAt = t2.UTC()
	}
	return c, nil
}

// ApplyLedgerEvent is Crier's idempotent projection primitive. In a
// single transaction it:
//
//  1. Reads crier_state.checkpoint.
//  2. If offset > checkpoint, projects the event by inserting one
//     row into op_events with ledger_offset = offset.
//  3. Updates crier_state.checkpoint to offset (and the timestamp).
//
// If offset <= checkpoint the call is a no-op (idempotent: re-applying
// the same offset cannot double-write). The UNIQUE index on
// op_events(ledger_offset) is a belt-and-braces guarantee.
//
// Returns (applied bool, err): applied=true when a new row was
// inserted; applied=false when the call was a no-op.
func (l *Librarian) ApplyLedgerEvent(
	offset int64,
	eventType string,
	sessionID string,
	createdAt time.Time,
	payload map[string]any,
) (bool, error) {
	if eventType == "" {
		return false, fmt.Errorf("ApplyLedgerEvent: event_type: %w", ErrEmptyArg)
	}
	if sessionID == "" {
		return false, fmt.Errorf("ApplyLedgerEvent: session_id: %w", ErrEmptyArg)
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var payloadJSON sql.NullString
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return false, fmt.Errorf("ApplyLedgerEvent marshal: %w", err)
		}
		payloadJSON = sql.NullString{String: string(b), Valid: true}
	}

	var applied bool
	err := l.v.Tx(func(tx *sql.Tx) error {
		var checkpoint int64
		if err := tx.QueryRow(`SELECT checkpoint FROM crier_state WHERE id = 1`).Scan(&checkpoint); err != nil {
			return fmt.Errorf("read checkpoint: %w", err)
		}
		if offset <= checkpoint {
			// Already applied — no-op. Checkpoint stays.
			return nil
		}
		// Insert op_events row with ledger_offset; the UNIQUE index
		// catches any racing double-insert and the OR IGNORE turns it
		// into a no-op (still idempotent if the unique constraint
		// fires under contention).
		res, err := tx.Exec(
			`INSERT OR IGNORE INTO op_events (event_type, session_id, created_at, payload, ledger_offset)
			 VALUES (?, ?, ?, ?, ?)`,
			eventType, sessionID, formatTime(createdAt),
			payloadJSON, offset,
		)
		if err != nil {
			return fmt.Errorf("insert op_event: %w", err)
		}
		rows, _ := res.RowsAffected()
		applied = rows > 0
		if _, err := tx.Exec(
			`UPDATE crier_state SET checkpoint = ?, updated_at = datetime('now') WHERE id = 1`,
			offset,
		); err != nil {
			return fmt.Errorf("update checkpoint: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ErrNoCheckpoint is returned by GetCrierCheckpoint when the
// crier_state row is missing — indicates an aborted migration or
// schema corruption.
var ErrNoCheckpoint = errors.New("crier_state row missing")
