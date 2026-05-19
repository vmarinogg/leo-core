package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/momhq/mom/cli/internal/vault"
	"github.com/spf13/cobra"
)

const centralExportFormat = "mom-central-vault-json-v1"

var centralExportTables = []string{
	"schema_migrations",
	"memories",
	"tags",
	"entities",
	"filter_audit",
	"op_events",
	"legacy_imports",
	"legacy_log_imports",
	"memory_tags",
	"memory_entities",
	"legacy_import_items",
	"legacy_log_import_items",
}

type centralExportManifest struct {
	Format    string         `json:"format"`
	CreatedAt string         `json:"created_at"`
	Tables    map[string]int `json:"tables"`
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the central MOM vault to JSON table files",
	Args:  cobra.NoArgs,
	RunE:  runExport,
}

var importCmd = &cobra.Command{
	Use:   "import <path>",
	Short: "Import MOM memory from a central export",
	Args:  cobra.ExactArgs(1),
	RunE:  runImport,
}

// runExport implements `mom export`. It writes a table-per-file JSON dump of
// the central SQLite vault under $HOME/.mom/exports/<timestamp>/.
func runExport(cmd *cobra.Command, _ []string) error {
	centralDir, err := centralvault.Dir()
	if err != nil {
		return err
	}
	v, err := centralvault.Open()
	if err != nil {
		return fmt.Errorf("open central vault: %w", err)
	}
	defer func() { _ = v.Close() }()

	stamp := time.Now().UTC().Format("20060102-150405Z")
	outDir := filepath.Join(centralDir, "exports", stamp)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return fmt.Errorf("creating export dir: %w", err)
	}

	manifest := centralExportManifest{
		Format:    centralExportFormat,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Tables:    map[string]int{},
	}

	for _, table := range centralExportTables {
		count, err := exportTable(v, table, filepath.Join(outDir, table+".json"))
		if err != nil {
			return fmt.Errorf("export %s: %w", table, err)
		}
		manifest.Tables[table] = count
	}
	manifestPath := filepath.Join(outDir, "manifest.json")
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}

	p := ux.NewPrinter(cmd.OutOrStdout())
	p.Diamond("export")
	p.Blank()
	for _, table := range centralExportTables {
		p.Chevron(fmt.Sprintf("%s: %d rows", table, manifest.Tables[table]))
	}
	p.Blank()
	p.Checkf("exported to %s", p.HighlightValue(outDir))
	return nil
}

func exportTable(v *vault.Vault, table, path string) (int, error) {
	var rowsOut []map[string]any
	err := v.Query("SELECT * FROM "+table, nil, func(rows *sql.Rows) error {
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			row := make(map[string]any, len(cols))
			for i, col := range cols {
				row[col] = jsonValue(vals[i])
			}
			rowsOut = append(rowsOut, row)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if rowsOut == nil {
		rowsOut = []map[string]any{}
	}
	return len(rowsOut), writeJSONFile(path, rowsOut)
}

func jsonValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// runImport implements `mom import <path>`. It is merge-only: existing rows are
// skipped, never overwritten. It accepts central table exports only.
func runImport(cmd *cobra.Command, args []string) error {
	importPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolving import path: %w", err)
	}

	p := ux.NewPrinter(cmd.OutOrStdout())
	p.Diamond("import")
	p.Blank()

	if ok, err := isCentralExport(importPath); err != nil {
		return err
	} else if ok {
		result, err := importCentralExport(importPath)
		if err != nil {
			return err
		}
		p.Checkf("%d rows imported", result.Imported)
		if result.Skipped > 0 {
			p.Muted(fmt.Sprintf("  %d skipped (already exist)", result.Skipped))
		}
		return nil
	}

	return fmt.Errorf("import path must be a MOM central export; legacy JSON imports were removed in v0.40 (upgrade to MOM v0.30 first for pre-v0.30 migrations)")
}

func isCentralExport(path string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading manifest: %w", err)
	}
	var manifest centralExportManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false, fmt.Errorf("parsing manifest: %w", err)
	}
	return manifest.Format == centralExportFormat, nil
}

type tableImportResult struct {
	Imported int
	Skipped  int
}

func importCentralExport(path string) (tableImportResult, error) {
	v, err := centralvault.Open()
	if err != nil {
		return tableImportResult{}, fmt.Errorf("open central vault: %w", err)
	}
	defer func() { _ = v.Close() }()

	var total tableImportResult
	for _, table := range centralExportTables {
		file := filepath.Join(path, table+".json")
		if _, err := os.Stat(file); os.IsNotExist(err) {
			continue
		}
		res, err := importTable(v, table, file)
		if err != nil {
			return tableImportResult{}, fmt.Errorf("import %s: %w", table, err)
		}
		total.Imported += res.Imported
		total.Skipped += res.Skipped
	}
	return total, nil
}

func importTable(v *vault.Vault, table, path string) (tableImportResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tableImportResult{}, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil {
		return tableImportResult{}, err
	}
	cols, err := tableColumns(v, table)
	if err != nil {
		return tableImportResult{}, err
	}
	var result tableImportResult
	err = v.Tx(func(tx *sql.Tx) error {
		for _, row := range rows {
			usedCols := make([]string, 0, len(cols))
			vals := make([]any, 0, len(cols))
			for _, col := range cols {
				if val, ok := row[col]; ok {
					usedCols = append(usedCols, col)
					vals = append(vals, val)
				}
			}
			if len(usedCols) == 0 {
				continue
			}
			placeholders := strings.TrimRight(strings.Repeat("?,", len(usedCols)), ",")
			stmt := fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES (%s)", table, strings.Join(usedCols, ","), placeholders)
			exec, err := tx.Exec(stmt, vals...)
			if err != nil {
				return err
			}
			affected, _ := exec.RowsAffected()
			if affected == 0 {
				result.Skipped++
			} else {
				result.Imported++
			}
		}
		return nil
	})
	return result, err
}

func tableColumns(v *vault.Vault, table string) ([]string, error) {
	var cols []string
	err := v.Query("PRAGMA table_info("+table+")", nil, func(rows *sql.Rows) error {
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull int
			var defaultValue any
			var pk int
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				return err
			}
			cols = append(cols, name)
		}
		return nil
	})
	return cols, err
}
