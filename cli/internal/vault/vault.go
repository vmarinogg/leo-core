// Package vault is the private SQLite primitive for v0.30. It owns a
// SQLite connection, runs schema migrations registered by callers, and
// mediates every read and write so other packages never touch *sql.DB
// directly. Canonical $HOME/.mom/mom.db path resolution lives in the
// centralvault package.
//
// Vault is the bottom of the v0.30 stack. Librarian is the domain-facing
// API over it; centralvault is the shared opener for production surfaces.
// Drafter, Logbook, Cartographer, Finder, MCP, Upgrade, and Lens go
// through Librarian. Vault has no knowledge of memories, tags, entities,
// or any other domain table — those tables and their migrations are
// owned by their respective packages and registered with this runner.
package vault

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

// ErrClosed is returned by Tx and Query when called on a Vault whose
// Close has already run. Defends against nil-pointer panics in the
// underlying *sql.DB; callers that hold a stale Vault reference get a
// clear error rather than a stack trace.
var ErrClosed = errors.New("vault: closed")

// Migration is one applied schema change identified by a strictly
// increasing integer version. Stmts run as a single transaction; either
// every statement commits and a row is added to schema_migrations, or
// nothing changes.
type Migration struct {
	Version int
	Stmts   []string
}

// Vault is the SQLite-backed persistence handle. The zero value is not
// usable; construct via Open. Vault is safe for concurrent use; the
// underlying *sql.DB is a connection pool.
type Vault struct {
	db *sql.DB
}

// dsnPragmas are SQLite pragmas embedded in the connection string so
// modernc.org/sqlite applies them on EVERY new connection in the pool.
// Per-connection pragmas (foreign_keys, synchronous, cache_size,
// busy_timeout) do not persist across pool growth when applied via
// db.Exec; embedding them in the DSN is the documented fix.
//
// WAL mode is database-wide (persists in the file) and is set
// separately via db.Exec after Open since it is not a per-connection
// setting.
const dsnPragmas = "?_pragma=foreign_keys(1)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=cache_size(-8000)" +
	"&_pragma=busy_timeout(5000)"

// Open opens or creates the SQLite database at path, applies pragmas,
// runs any pending registered migrations, and returns a usable Vault.
// Callers must Close the Vault when done.
//
// On POSIX systems, the database file is created (or chmod'd if it
// already exists) with mode 0600. modernc.org/sqlite does not expose a
// perms DSN parameter, so the file is materialized here before
// sql.Open.
//
// Migrations must be sorted by Version with no duplicates. Open
// validates this before any DB I/O. Each migration runs in its own
// transaction; a failure leaves the database at the previous
// successfully-applied version with no partial DDL or schema_migrations
// row from the failed migration.
//
// Open auto-runs migrations: a returned Vault is always at the current
// schema version. There is no opt-out — every consumer wants a migrated
// vault.
func Open(path string, migrations []Migration) (*Vault, error) {
	if err := validateMigrations(migrations); err != nil {
		return nil, err
	}

	if runtime.GOOS != "windows" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("creating vault file at %s: %w", path, err)
		}
		_ = f.Close()
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("chmod %s to 0600: %w", path, err)
		}
	}

	db, err := sql.Open("sqlite", path+dsnPragmas)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite at %s: %w", path, err)
	}
	// WAL is database-wide — set once via Exec; persists in the file.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("setting journal_mode=WAL: %w", err)
	}

	v := &Vault{db: db}
	if err := v.migrate(migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrating vault at %s: %w", path, err)
	}
	return v, nil
}

// Close releases the underlying database connection. Idempotent —
// calling Close on an already-closed Vault is a no-op.
func (v *Vault) Close() error {
	if v.db == nil {
		return nil
	}
	err := v.db.Close()
	v.db = nil
	return err
}

// validateMigrations enforces strictly increasing positive versions
// with no duplicates and at least one statement per migration. Pure
// function, called before any DB I/O so a bad slice fails fast and
// leaves no half-initialised vault.
//
// Rejecting Version <= 0 reserves zero/negative version space; future
// internal bookkeeping can use it without ambiguity. Rejecting empty
// Stmts catches the placeholder-migration footgun where a caller
// forgets to fill in the body.
func validateMigrations(ms []Migration) error {
	for i, m := range ms {
		if m.Version <= 0 {
			return fmt.Errorf("migration version %d is non-positive (must be > 0)", m.Version)
		}
		if len(m.Stmts) == 0 {
			return fmt.Errorf("migration version %d has no statements", m.Version)
		}
		if i > 0 && m.Version <= ms[i-1].Version {
			return fmt.Errorf("migration version %d follows %d (must be strictly increasing)",
				m.Version, ms[i-1].Version)
		}
	}
	return nil
}

// migrate ensures schema_migrations exists, then applies every
// registered migration whose version is not already recorded. Each
// migration runs inside its own transaction.
func (v *Vault) migrate(migrations []Migration) error {
	if _, err := v.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	for _, m := range migrations {
		var applied int
		if err := v.db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
			m.Version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("checking migration %d: %w", m.Version, err)
		}
		if applied > 0 {
			continue
		}
		if err := v.Tx(func(tx *sql.Tx) error {
			for _, stmt := range m.Stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("statement: %w", err)
				}
			}
			_, err := tx.Exec(
				`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
				m.Version,
				time.Now().UTC().Format(time.RFC3339Nano),
			)
			return err
		}); err != nil {
			return fmt.Errorf("applying migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// Query runs a read-only query and invokes scan with the resulting
// rows. Vault closes the *sql.Rows after scan returns. Callers never
// see *sql.DB.
//
// Returns ErrClosed if Close has already run on the Vault.
func (v *Vault) Query(query string, args []any, scan func(*sql.Rows) error) error {
	if v.db == nil {
		return ErrClosed
	}
	rows, err := v.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := scan(rows); err != nil {
		return err
	}
	return rows.Err()
}

// Tx runs fn inside a transaction. The transaction commits on nil
// return; any non-nil error rolls it back and is returned to the
// caller. A panic in fn rolls the transaction back and re-panics.
// Callers receive a *sql.Tx but never *sql.DB.
//
// Returns ErrClosed if Close has already run on the Vault.
func (v *Vault) Tx(fn func(*sql.Tx) error) (err error) {
	if v.db == nil {
		return ErrClosed
	}
	tx, err := v.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	err = fn(tx)
	return err
}
