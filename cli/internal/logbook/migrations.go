package logbook

import "github.com/momhq/mom/cli/internal/librarian"

// Migrations returns the schema migrations Logbook owns. Callers
// concatenate these with Librarian's migrations and pass the combined
// list to vault.Open. The list is exposed via librarian.Migration to
// keep "only Librarian imports vault" auditable.
//
// Migration 3 creates the operational stream table. There is NO
// separate `event_log` table; this is the single source of truth for
// "what MOM did" and what `mom lens` reads.
func Migrations() []librarian.Migration {
	return []librarian.Migration{
		{
			Version: 3,
			Stmts: []string{
				`CREATE TABLE op_events (
					id          INTEGER PRIMARY KEY AUTOINCREMENT,
					event_type  TEXT NOT NULL,
					session_id  TEXT NOT NULL,
					created_at  TEXT NOT NULL,
					payload     TEXT
				)`,
				// idx_op_events_type serves cross-session queries
				// ("all events of type X"). idx_op_events_session_time
				// is composite — its leading column matches "events
				// for session X" alone, and the trailing created_at
				// DESC matches "events for session X in last N hours,"
				// the most common mom lens query. Single-column
				// indexes on session_id and created_at would each be
				// subsumed by this composite or rarely queried alone.
				`CREATE INDEX idx_op_events_type         ON op_events(event_type)`,
				`CREATE INDEX idx_op_events_session_time ON op_events(session_id, created_at DESC)`,
			},
		},
	}
}
