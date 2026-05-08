package vault_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/vault"
)

// helper opens a vault at a temp path with the given migrations, registers
// t.Cleanup for Close, and fails the test if Open errors.
func openTempVault(t *testing.T, migrations []vault.Migration) (*vault.Vault, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mom.db")
	v, err := vault.Open(path, migrations)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	return v, path
}

func TestOpen_createsSchemaMigrationsTable(t *testing.T) {
	v, _ := openTempVault(t, nil)

	var name string
	err := v.Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
		nil,
		func(rs *sql.Rows) error {
			if !rs.Next() {
				return errors.New("schema_migrations table not found")
			}
			return rs.Scan(&name)
		},
	)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if name != "schema_migrations" {
		t.Fatalf("got %q, want schema_migrations", name)
	}
}

func TestOpen_createsFileWith0600OnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX perms only")
	}
	_, path := openTempVault(t, nil)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("got mode %o, want 0600", mode)
	}
}

func TestOpen_runsRegisteredMigrations(t *testing.T) {
	migs := []vault.Migration{
		{Version: 1, Stmts: []string{`CREATE TABLE widgets (id INTEGER PRIMARY KEY)`}},
	}
	v, _ := openTempVault(t, migs)

	if err := v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO widgets (id) VALUES (1)`)
		return err
	}); err != nil {
		t.Fatalf("insert into migrated table: %v", err)
	}

	var count int
	if err := v.Query(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`,
		nil,
		func(rs *sql.Rows) error {
			rs.Next()
			return rs.Scan(&count)
		},
	); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations count = %d, want 1", count)
	}
}

func TestOpen_isIdempotent(t *testing.T) {
	migs := []vault.Migration{
		{Version: 1, Stmts: []string{`CREATE TABLE widgets (id INTEGER PRIMARY KEY)`}},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mom.db")

	// First open: apply migration and write a row to the migrated
	// table. The row + the table itself must survive close/re-open.
	v1, err := vault.Open(path, migs)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := v1.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO widgets (id) VALUES (42)`)
		return err
	}); err != nil {
		t.Fatalf("seed widgets: %v", err)
	}
	_ = v1.Close()

	// Re-open: must be a no-op for already-applied migrations AND
	// must leave the previously-written data untouched. Checking
	// only schema_migrations would miss a regression where the
	// re-open path accidentally drops user tables.
	v2, err := vault.Open(path, migs)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = v2.Close() })

	var count int
	if err := v2.Query(
		`SELECT COUNT(*) FROM schema_migrations`,
		nil,
		func(rs *sql.Rows) error { rs.Next(); return rs.Scan(&count) },
	); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations count = %d after re-open, want 1", count)
	}

	// DDL from migration 1 still present and queryable.
	var widgetID int
	if err := v2.Query(
		`SELECT id FROM widgets`,
		nil,
		func(rs *sql.Rows) error { rs.Next(); return rs.Scan(&widgetID) },
	); err != nil {
		t.Fatalf("query widgets after re-open: %v", err)
	}
	if widgetID != 42 {
		t.Fatalf("widgets row = %d, want 42 (re-open dropped data?)", widgetID)
	}
}

func TestMigrate_atomicPerMigration_rollsBackBothDDLAndVersionRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mom.db")

	bad := []vault.Migration{
		{Version: 1, Stmts: []string{
			`CREATE TABLE good (id INTEGER PRIMARY KEY)`,
			`THIS IS NOT VALID SQL`,
		}},
	}
	_, err := vault.Open(path, bad)
	if err == nil {
		t.Fatal("expected error from invalid migration, got nil")
	}

	// Re-open with a clean migrations list to inspect post-failure state.
	v, err := vault.Open(path, nil)
	if err != nil {
		t.Fatalf("re-open after failed migration: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })

	// The "good" table from the first statement must NOT exist — the whole
	// migration rolled back atomically.
	var tableName string
	err = v.Query(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='good'`,
		nil,
		func(rs *sql.Rows) error {
			if rs.Next() {
				return rs.Scan(&tableName)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("query for good table: %v", err)
	}
	if tableName != "" {
		t.Fatal("CREATE TABLE good was not rolled back after migration failure")
	}

	// And no version-1 row exists in schema_migrations.
	var versionCount int
	if err := v.Query(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 1`,
		nil,
		func(rs *sql.Rows) error { rs.Next(); return rs.Scan(&versionCount) },
	); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if versionCount != 0 {
		t.Fatal("schema_migrations recorded version 1 despite failure")
	}
}

func TestOpen_rejectsNonMonotonicMigrations(t *testing.T) {
	bad := []vault.Migration{
		{Version: 2, Stmts: []string{`CREATE TABLE a(id INT)`}},
		{Version: 1, Stmts: []string{`CREATE TABLE b(id INT)`}},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mom.db")
	_, err := vault.Open(path, bad)
	if err == nil {
		t.Fatal("expected error for non-monotonic migrations, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("error %q should mention version ordering", err)
	}
}

func TestOpen_rejectsZeroOrNegativeVersion(t *testing.T) {
	for _, version := range []int{0, -1} {
		bad := []vault.Migration{
			{Version: version, Stmts: []string{`CREATE TABLE a(id INT)`}},
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "mom.db")
		_, err := vault.Open(path, bad)
		if err == nil {
			t.Errorf("Version=%d: expected error, got nil", version)
		}
	}
}

func TestOpen_rejectsEmptyStatements(t *testing.T) {
	bad := []vault.Migration{
		{Version: 1, Stmts: nil},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mom.db")
	_, err := vault.Open(path, bad)
	if err == nil {
		t.Fatal("expected error for migration with no statements, got nil")
	}
}

func TestOpen_rejectsDuplicateMigrationVersions(t *testing.T) {
	bad := []vault.Migration{
		{Version: 1, Stmts: []string{`CREATE TABLE a(id INT)`}},
		{Version: 1, Stmts: []string{`CREATE TABLE b(id INT)`}},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mom.db")
	_, err := vault.Open(path, bad)
	if err == nil {
		t.Fatal("expected error for duplicate migration versions, got nil")
	}
}

func TestForeignKeysEnforcedAcrossPoolGrowth(t *testing.T) {
	migs := []vault.Migration{
		{Version: 1, Stmts: []string{
			`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
			`CREATE TABLE child (
				id INTEGER PRIMARY KEY,
				parent_id INTEGER NOT NULL REFERENCES parent(id)
			)`,
		}},
	}
	v, _ := openTempVault(t, migs)

	// Force the pool to grow at least one extra connection by holding a
	// transaction open while issuing a read on a separate goroutine. Then
	// probe FK enforcement on a fresh connection.
	done := make(chan error, 1)
	hold := make(chan struct{})
	release := make(chan struct{})
	go func() {
		done <- v.Tx(func(tx *sql.Tx) error {
			close(hold)
			<-release
			return nil
		})
	}()
	<-hold
	defer func() { close(release); <-done }()

	// On a different (fresh) pool connection, attempt an FK violation.
	// Foreign keys must still be enforced.
	err := v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO child (id, parent_id) VALUES (1, 999)`)
		return err
	})
	if err == nil {
		t.Fatal("expected FK violation error on fresh pool connection, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("expected foreign-key error, got: %v", err)
	}
}

func TestTx_commitsOnNilReturn(t *testing.T) {
	migs := []vault.Migration{
		{Version: 1, Stmts: []string{`CREATE TABLE t (id INTEGER PRIMARY KEY)`}},
	}
	v, _ := openTempVault(t, migs)

	if err := v.Tx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO t (id) VALUES (1)`)
		return err
	}); err != nil {
		t.Fatalf("Tx: %v", err)
	}

	var count int
	_ = v.Query(`SELECT COUNT(*) FROM t`, nil, func(rs *sql.Rows) error {
		rs.Next()
		return rs.Scan(&count)
	})
	if count != 1 {
		t.Fatalf("count = %d, want 1 (commit failed)", count)
	}
}

func TestTx_rollsBackOnError(t *testing.T) {
	migs := []vault.Migration{
		{Version: 1, Stmts: []string{`CREATE TABLE t (id INTEGER PRIMARY KEY)`}},
	}
	v, _ := openTempVault(t, migs)

	wantErr := errors.New("boom")
	got := v.Tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO t (id) VALUES (1)`); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(got, wantErr) {
		t.Fatalf("Tx returned %v, want %v wrapped", got, wantErr)
	}

	var count int
	_ = v.Query(`SELECT COUNT(*) FROM t`, nil, func(rs *sql.Rows) error {
		rs.Next()
		return rs.Scan(&count)
	})
	if count != 0 {
		t.Fatalf("count = %d, want 0 (rollback failed)", count)
	}
}

func TestTx_rollsBackOnPanic(t *testing.T) {
	migs := []vault.Migration{
		{Version: 1, Stmts: []string{`CREATE TABLE t (id INTEGER PRIMARY KEY)`}},
	}
	v, _ := openTempVault(t, migs)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to propagate")
		}
		var count int
		_ = v.Query(`SELECT COUNT(*) FROM t`, nil, func(rs *sql.Rows) error {
			rs.Next()
			return rs.Scan(&count)
		})
		if count != 0 {
			t.Fatalf("count = %d, want 0 (panic rollback failed)", count)
		}
	}()

	_ = v.Tx(func(tx *sql.Tx) error {
		_, _ = tx.Exec(`INSERT INTO t (id) VALUES (1)`)
		panic("boom")
	})
}

func TestClose_isIdempotent(t *testing.T) {
	v, _ := openTempVault(t, nil)
	if err := v.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestUseAfterClose_ReturnsErrClosedNotPanic locks the contract that
// using a closed Vault returns vault.ErrClosed instead of nil-pointer
// panicking through the underlying *sql.DB. Callers that hold a stale
// Vault (e.g., via a Librarian) get a recoverable error.
func TestUseAfterClose_ReturnsErrClosedNotPanic(t *testing.T) {
	v, _ := openTempVault(t, nil)
	if err := v.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := v.Tx(func(tx *sql.Tx) error { return nil }); !errors.Is(err, vault.ErrClosed) {
		t.Errorf("Tx after Close: err = %v, want ErrClosed", err)
	}
	if err := v.Query(`SELECT 1`, nil, func(*sql.Rows) error { return nil }); !errors.Is(err, vault.ErrClosed) {
		t.Errorf("Query after Close: err = %v, want ErrClosed", err)
	}
}
