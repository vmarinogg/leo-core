package librarian

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// OpEvent is one row in Logbook's operational stream. It is the
// substance for `mom lens` activity timelines and the "what MOM did"
// audit surface.
//
// CreatedAt is stored using the fixed-width nanosecond format
// Librarian uses for every owned timestamp.
type OpEvent struct {
	ID        int64
	EventType string
	SessionID string
	CreatedAt time.Time
	Payload   map[string]any
}

// OpEventFilter narrows a QueryOpEvents call. Zero values mean "no
// filter on that dimension". Limit defaults to 100 if zero. Until is
// exclusive; Since is inclusive.
type OpEventFilter struct {
	EventType string
	SessionID string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// InsertOpEvent appends a row to the operational stream. EventType and
// SessionID are required end-to-end (NOT NULL at the schema, rejected
// at the API). Synthetic session IDs (`mom-<uuid>`) are valid for
// MOM-internal runs (cartographer, import); empty is always a
// programming error.
//
// Payload normalisation: nil and empty map both serialise to JSON
// "null". On read, json.Unmarshal of "null" into a map sets the target
// to nil, so round-trip is symmetrical and no special-case guard is
// required.
func (l *Librarian) InsertOpEvent(e OpEvent) (int64, error) {
	if strings.TrimSpace(e.EventType) == "" {
		return 0, fmt.Errorf("InsertOpEvent: event_type: %w", ErrEmptyArg)
	}
	if strings.TrimSpace(e.SessionID) == "" {
		return 0, fmt.Errorf("InsertOpEvent: session_id: %w", ErrEmptyArg)
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = l.now()
	}
	// nil and empty map both serialize to "null".
	var payload map[string]any
	if len(e.Payload) > 0 {
		payload = e.Payload
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("InsertOpEvent marshal: %w", err)
	}

	var id int64
	err = l.v.Tx(func(tx *sql.Tx) error {
		res, execErr := tx.Exec(
			`INSERT INTO op_events (event_type, session_id, created_at, payload)
			 VALUES (?, ?, ?, ?)`,
			e.EventType,
			e.SessionID,
			formatTime(e.CreatedAt),
			string(payloadJSON),
		)
		if execErr != nil {
			return execErr
		}
		id, execErr = res.LastInsertId()
		return execErr
	})
	if err != nil {
		return 0, fmt.Errorf("InsertOpEvent: %w", err)
	}
	return id, nil
}

// QueryOpEvents returns rows matching the filter, ordered by created_at
// descending (most recent first), then id descending as a tiebreaker.
// Limit defaults to 100.
func (l *Librarian) QueryOpEvents(f OpEventFilter) ([]OpEvent, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	var (
		sb   strings.Builder
		args []any
	)
	sb.WriteString(`SELECT id, event_type, session_id, created_at, payload
		FROM op_events WHERE 1=1`)
	if f.EventType != "" {
		sb.WriteString(` AND event_type = ?`)
		args = append(args, f.EventType)
	}
	if f.SessionID != "" {
		sb.WriteString(` AND session_id = ?`)
		args = append(args, f.SessionID)
	}
	if !f.Since.IsZero() {
		sb.WriteString(` AND created_at >= ?`)
		args = append(args, formatTime(f.Since))
	}
	if !f.Until.IsZero() {
		sb.WriteString(` AND created_at < ?`)
		args = append(args, formatTime(f.Until))
	}
	sb.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ?`)
	args = append(args, limit)

	out := []OpEvent{}
	err := l.v.Query(sb.String(), args, func(rs *sql.Rows) error {
		for rs.Next() {
			var (
				e            OpEvent
				createdAtStr string
				payloadStr   sql.NullString
			)
			if err := rs.Scan(&e.ID, &e.EventType, &e.SessionID, &createdAtStr, &payloadStr); err != nil {
				return err
			}
			t, err := parseTime(createdAtStr)
			if err != nil {
				return fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
			}
			e.CreatedAt = t
			if payloadStr.Valid && payloadStr.String != "" {
				if err := json.Unmarshal([]byte(payloadStr.String), &e.Payload); err != nil {
					return fmt.Errorf("unmarshal payload: %w", err)
				}
			}
			out = append(out, e)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("QueryOpEvents: %w", err)
	}
	return out, nil
}
