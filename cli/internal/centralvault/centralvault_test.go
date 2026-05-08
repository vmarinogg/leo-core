package centralvault_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/centralvault"
)

func TestPathUsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", "")

	got, err := centralvault.Path()
	if err != nil {
		t.Fatalf("Path returned error: %v", err)
	}

	want := filepath.Join(home, ".mom", "mom.db")
	if got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}

func TestPathUsesMomVaultOverride(t *testing.T) {
	home := t.TempDir()
	override := filepath.Join(t.TempDir(), "custom", "mom-v030.db")
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", override)

	got, err := centralvault.Path()
	if err != nil {
		t.Fatalf("Path returned error: %v", err)
	}
	if got != override {
		t.Fatalf("Path = %q, want MOM_VAULT override %q", got, override)
	}

	dir, err := centralvault.Dir()
	if err != nil {
		t.Fatalf("Dir returned error: %v", err)
	}
	if dir != filepath.Dir(override) {
		t.Fatalf("Dir = %q, want %q", dir, filepath.Dir(override))
	}
}

func TestOpenLibrarianCreatesOnlyTempHomeVault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", "")

	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		t.Fatalf("OpenLibrarian returned error: %v", err)
	}
	if lib == nil {
		t.Fatal("OpenLibrarian returned nil Librarian")
	}
	defer func() { _ = closeFn() }()

	dbPath := filepath.Join(home, ".mom", "mom.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("central vault not created at temp HOME path %s: %v", dbPath, err)
	}

	for _, sidecar := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if strings.HasPrefix(sidecar, home) {
			continue
		}
		t.Fatalf("sidecar path escaped temp HOME: %s", sidecar)
	}
}

func TestOpenRunsFullCentralMigrations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", "")

	v, err := centralvault.Open()
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = v.Close() }()

	for _, table := range []string{"memories", "tags", "entities", "op_events", "filter_audit"} {
		table := table
		err := v.Query(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			[]any{table},
			func(rows *sql.Rows) error {
				if !rows.Next() {
					t.Fatalf("table %s not found", table)
				}
				var name string
				if err := rows.Scan(&name); err != nil {
					return err
				}
				if name != table {
					t.Fatalf("got table %q, want %q", name, table)
				}
				return nil
			},
		)
		if err != nil {
			t.Fatalf("querying table %s: %v", table, err)
		}
	}
}

func TestOpenCreatesMomVaultOverrideParent(t *testing.T) {
	home := t.TempDir()
	override := filepath.Join(t.TempDir(), "nested", "mom-v030.db")
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", override)

	v, err := centralvault.Open()
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() { _ = v.Close() }()

	if _, err := os.Stat(override); err != nil {
		t.Fatalf("MOM_VAULT database not created at %s: %v", override, err)
	}
	defaultPath := filepath.Join(home, ".mom", "mom.db")
	if _, err := os.Stat(defaultPath); !os.IsNotExist(err) {
		t.Fatalf("default HOME vault should not be touched when MOM_VAULT is set: %s", defaultPath)
	}
}

func TestNoCentralVaultPathAssemblyOutsideHelper(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	internalDir := filepath.Dir(filepath.Dir(file))
	allowedDir := filepath.Dir(file)

	patterns := []string{
		`filepath.Join(home, ".mom", "mom.db")`,
		`filepath.Join(momHome, "mom.db")`,
		`os.Getenv("MOM_VAULT")`,
	}

	err := filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		if filepath.Dir(path) == allowedDir {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, p := range patterns {
			if strings.Contains(text, p) {
				t.Fatalf("central vault path assembly %q found outside helper in %s", p, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal dir: %v", err)
	}
}
