// Package centralvault resolves and opens MOM's canonical v0.30 vault.
package centralvault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
)

const (
	dbName       = "mom.db"
	envVaultPath = "MOM_VAULT"
)

// Dir returns the directory containing MOM's canonical central vault.
// If MOM_VAULT is set, Dir returns that file's parent directory.
// Otherwise it returns $HOME/.mom.
func Dir() (string, error) {
	if override := os.Getenv(envVaultPath); override != "" {
		return filepath.Dir(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve $HOME: %w", err)
	}
	return filepath.Join(home, ".mom"), nil
}

// Path returns MOM's canonical central vault path. MOM_VAULT overrides
// the default $HOME/.mom/mom.db location for local testing and contributor
// workflows.
func Path() (string, error) {
	if override := os.Getenv(envVaultPath); override != "" {
		return override, nil
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, dbName), nil
}

// Migrations returns the full v0.30 central-vault migration set.
func Migrations() []vault.Migration {
	migs := append([]vault.Migration{}, librarian.Migrations()...)
	migs = append(migs, logbook.Migrations()...)
	migs = append(migs, vault.Migration{
		Version: 4,
		Stmts: []string{
			`CREATE TABLE legacy_imports (
				source_path        TEXT PRIMARY KEY,
				source_fingerprint TEXT NOT NULL,
				imported_at        TEXT NOT NULL,
				memory_count       INTEGER NOT NULL,
				mapping_count      INTEGER NOT NULL
			)`,
			`CREATE TABLE legacy_import_items (
				source_path  TEXT NOT NULL,
				old_id       TEXT NOT NULL,
				new_id       TEXT NOT NULL,
				content_hash TEXT NOT NULL,
				created_at   TEXT NOT NULL,
				PRIMARY KEY (source_path, old_id),
				FOREIGN KEY (source_path) REFERENCES legacy_imports(source_path)
			)`,
		},
	})
	migs = append(migs, vault.Migration{
		Version: 5,
		Stmts: []string{
			`CREATE TABLE legacy_log_imports (
				source_path        TEXT PRIMARY KEY,
				source_fingerprint TEXT NOT NULL,
				imported_at        TEXT NOT NULL,
				event_count        INTEGER NOT NULL,
				mapping_count      INTEGER NOT NULL
			)`,
			`CREATE TABLE legacy_log_import_items (
				source_path    TEXT NOT NULL,
				source_item_id TEXT NOT NULL,
				op_event_id    INTEGER NOT NULL,
				content_hash   TEXT NOT NULL,
				event_type     TEXT NOT NULL,
				session_id     TEXT NOT NULL,
				created_at     TEXT NOT NULL,
				PRIMARY KEY (source_path, source_item_id),
				FOREIGN KEY (source_path) REFERENCES legacy_log_imports(source_path),
				FOREIGN KEY (op_event_id) REFERENCES op_events(id)
			)`,
		},
	})
	sort.Slice(migs, func(i, j int) bool { return migs[i].Version < migs[j].Version })
	return migs
}

// Open opens the central vault, creating $HOME/.mom when needed and
// applying all registered v0.30 migrations.
func Open() (*vault.Vault, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", dir, err)
	}
	v, err := vault.Open(path, Migrations())
	if err != nil {
		return nil, fmt.Errorf("vault.Open %s: %w", path, err)
	}
	return v, nil
}

// OpenLibrarian opens the central vault and returns a Librarian bound to it.
// The returned close function releases the underlying database handle.
func OpenLibrarian() (*librarian.Librarian, func() error, error) {
	v, err := Open()
	if err != nil {
		return nil, nil, err
	}
	return librarian.New(v), v.Close, nil
}
