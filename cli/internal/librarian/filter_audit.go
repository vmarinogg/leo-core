package librarian

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// FilterAuditRow is one row in the filter_audit table — a single
// secret-detection category and its lifetime fire count. Drafter
// bumps these via IncrementFilterAudit; Lens reads them via
// FilterAuditCounts. The matched substance is NEVER stored — only
// the category that fired and how often.
type FilterAuditRow struct {
	Category       string
	RedactionCount int64
	LastFiredAt    time.Time
}

// IncrementFilterAudit bumps the lifetime counter for a single
// secret-detection category by one and updates last_fired_at to now.
// Atomic UPSERT so concurrent calls (multiple Drafters across
// processes sharing a vault) stay consistent.
//
// The matched secret is intentionally not a parameter — Drafter
// passes the category, not the redacted substance. Compare to
// op_events which carries metadata only: filter_audit is the same
// shape, narrowed to "this kind of secret fired."
func (l *Librarian) IncrementFilterAudit(category string) error {
	if strings.TrimSpace(category) == "" {
		return fmt.Errorf("IncrementFilterAudit: category: %w", ErrEmptyArg)
	}
	now := formatTime(l.now())
	return l.v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO filter_audit (category, redaction_count, last_fired_at)
			 VALUES (?, 1, ?)
			 ON CONFLICT(category) DO UPDATE SET
			   redaction_count = redaction_count + 1,
			   last_fired_at   = excluded.last_fired_at`,
			category, now,
		)
		return err
	})
}

// FilterAuditCounts returns every filter_audit row, ordered by
// category. Lens consumes this for its "is the privacy filter
// firing?" panel.
func (l *Librarian) FilterAuditCounts() ([]FilterAuditRow, error) {
	out := []FilterAuditRow{}
	err := l.v.Query(
		`SELECT category, redaction_count, last_fired_at
		   FROM filter_audit
		  ORDER BY category`,
		nil,
		func(rs *sql.Rows) error {
			for rs.Next() {
				var (
					row     FilterAuditRow
					lastTxt sql.NullString
				)
				if err := rs.Scan(&row.Category, &row.RedactionCount, &lastTxt); err != nil {
					return err
				}
				if lastTxt.Valid && lastTxt.String != "" {
					t, err := parseTime(lastTxt.String)
					if err != nil {
						return fmt.Errorf("parse last_fired_at %q: %w", lastTxt.String, err)
					}
					row.LastFiredAt = t
				}
				out = append(out, row)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("FilterAuditCounts: %w", err)
	}
	return out, nil
}
