package librarian_test

import (
	"errors"
	"testing"
	"time"

	"github.com/momhq/mom/cli/internal/librarian"
)

func TestIncrementFilterAudit_CreatesAndIncrements(t *testing.T) {
	l := openLib(t)

	if err := l.IncrementFilterAudit("aws_key"); err != nil {
		t.Fatalf("first increment: %v", err)
	}
	if err := l.IncrementFilterAudit("aws_key"); err != nil {
		t.Fatalf("second increment: %v", err)
	}
	if err := l.IncrementFilterAudit("github_pat"); err != nil {
		t.Fatalf("github increment: %v", err)
	}

	rows, err := l.FilterAuditCounts()
	if err != nil {
		t.Fatalf("FilterAuditCounts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (aws_key, github_pat)", len(rows))
	}
	// Ordered by category: aws_key first, github_pat second.
	if rows[0].Category != "aws_key" || rows[0].RedactionCount != 2 {
		t.Errorf("aws_key row = %+v, want category=aws_key count=2", rows[0])
	}
	if rows[1].Category != "github_pat" || rows[1].RedactionCount != 1 {
		t.Errorf("github_pat row = %+v, want count=1", rows[1])
	}
	// last_fired_at is set on every increment (within a generous window).
	for _, row := range rows {
		if row.LastFiredAt.IsZero() {
			t.Errorf("category %q: LastFiredAt is zero", row.Category)
		}
		if time.Since(row.LastFiredAt) > 5*time.Second {
			t.Errorf("category %q: LastFiredAt %v looks stale", row.Category, row.LastFiredAt)
		}
	}
}

func TestIncrementFilterAudit_RejectsEmptyCategory(t *testing.T) {
	l := openLib(t)
	if err := l.IncrementFilterAudit(""); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestFilterAuditCounts_EmptyTableReturnsEmptySlice(t *testing.T) {
	l := openLib(t)
	rows, err := l.FilterAuditCounts()
	if err != nil {
		t.Fatalf("FilterAuditCounts: %v", err)
	}
	if rows == nil {
		t.Errorf("got nil, want non-nil empty slice")
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}
