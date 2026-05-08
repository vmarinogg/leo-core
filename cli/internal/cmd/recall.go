package cmd

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/momhq/mom/cli/internal/centralvault"
	"github.com/momhq/mom/cli/internal/finder"
	"github.com/momhq/mom/cli/internal/ux"
	"github.com/momhq/mom/cli/internal/vault"
	"github.com/spf13/cobra"
)

const recallDefaultLimit = 10

var recallCmd = &cobra.Command{
	Use:   "recall <query>",
	Short: "Search memories in the central vault",
	Long: `Search memories in the central vault.

The query argument is required. It can be a natural-language search query
or a read-only SQL SELECT/WITH query for power users.

Examples:
  mom recall "aws deployment flow"
  mom recall "SELECT id, summary FROM memories WHERE promotion_state = 'curated'"`,
	Args: cobra.ExactArgs(1),
	RunE: runRecall,
}

func runRecall(cmd *cobra.Command, args []string) error {
	query := strings.TrimSpace(args[0])
	if query == "" {
		return fmt.Errorf("query is required")
	}

	p := ux.NewPrinter(cmd.OutOrStdout())

	if isSQLQuery(query) {
		return runRecallSQL(cmd, query)
	}

	lib, closeFn, err := centralvault.OpenLibrarian()
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = closeFn() }()

	results, err := finder.New(lib).Recall(finder.Options{Query: query, Limit: recallDefaultLimit})
	if err != nil {
		if errors.Is(err, finder.ErrEmptyQuery) {
			return fmt.Errorf("query is required")
		}
		return fmt.Errorf("recall failed: %w", err)
	}

	if len(results) == 0 {
		p.Muted("No memories matched your query.")
		return nil
	}

	p.Diamond(fmt.Sprintf("recall %q — %d results", query, len(results)))
	p.Blank()
	p.Bold(fmt.Sprintf("%-36s  %-10s  %-12s  %s", "ID", "Score", "State", "Summary"))
	p.Muted(strings.Repeat("─", 92))
	for _, r := range results {
		landmark := ""
		if r.Landmark {
			landmark = p.HighlightValue(" ★")
		}
		p.Textf("%-36s  %s  %-12s  %s%s",
			truncate(r.ID, 36),
			p.HighlightValue(fmt.Sprintf("%-10.3f", r.Score)),
			r.PromotionState,
			truncate(r.Summary, 40),
			landmark,
		)
	}
	return nil
}

func runRecallSQL(cmd *cobra.Command, query string) error {
	if err := validateReadOnlySQL(query); err != nil {
		return err
	}
	path, err := centralvault.Path()
	if err != nil {
		return err
	}
	v, err := vault.Open(path, centralvault.Migrations())
	if err != nil {
		return fmt.Errorf("opening central vault: %w", err)
	}
	defer func() { _ = v.Close() }()

	rowsOut := []map[string]any{}
	err = v.Query(query, nil, func(rows *sql.Rows) error {
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
			for i, c := range cols {
				row[c] = sqlValue(vals[i])
			}
			rowsOut = append(rowsOut, row)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("sql recall failed: %w", err)
	}

	text, err := json.MarshalIndent(rowsOut, "", "  ")
	if err != nil {
		return fmt.Errorf("format sql results: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(text))
	return nil
}

func sqlValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}

func isSQLQuery(query string) bool {
	q := strings.TrimLeftFunc(query, unicode.IsSpace)
	upper := strings.ToUpper(q)
	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH") {
		return true
	}
	return sqlForbiddenStart.MatchString(upper)
}

var sqlForbiddenStart = regexp.MustCompile(`^(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|REPLACE|PRAGMA|ATTACH|DETACH|VACUUM|REINDEX)\b`)
var sqlForbidden = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|REPLACE|PRAGMA|ATTACH|DETACH|VACUUM|REINDEX)\b`)

func validateReadOnlySQL(query string) error {
	q := strings.TrimSpace(query)
	if q == "" {
		return fmt.Errorf("query is required")
	}
	trimmed := strings.TrimRight(q, " \t\r\n;")
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("SQL recall accepts one read-only statement")
	}
	upper := strings.ToUpper(strings.TrimLeftFunc(q, unicode.IsSpace))
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return fmt.Errorf("SQL recall only accepts SELECT/WITH queries")
	}
	if sqlForbidden.MatchString(q) {
		return fmt.Errorf("SQL recall is read-only")
	}
	return nil
}
