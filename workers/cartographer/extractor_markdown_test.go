package cartographer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMarkdownExtractor_Matches(t *testing.T) {
	e := NewMarkdownExtractor()

	tests := []struct {
		path string
		want bool
	}{
		{"README.md", true},
		{"docs/guide.mdx", true},
		{"notes.txt", true},
		{"spec.rst", true},
		{"main.go", false},
		{"package.json", false},
	}

	for _, tt := range tests {
		if got := e.Matches(tt.path); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMarkdownExtractor_Extract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "README.md"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewMarkdownExtractor()
	src := Source{
		Path:      "testdata/README.md",
		Content:   data,
		Extension: ".md",
	}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) == 0 {
		t.Fatal("expected at least one draft, got zero")
	}

	// Provenance must be set on all drafts.
	for _, d := range drafts {
		if d.Provenance.SourceFile == "" {
			t.Errorf("draft %q missing SourceFile provenance", d.Summary)
		}
		if d.Provenance.TriggerEvent != TriggerEvent {
			t.Errorf("draft %q has TriggerEvent %q, want %q", d.Summary, d.Provenance.TriggerEvent, TriggerEvent)
		}
	}
}

func TestMarkdownExtractor_HeadingDecision(t *testing.T) {
	content := []byte("# My Project\n\n## Decision\n\nWe chose PostgreSQL because it is robust.\n")
	e := NewMarkdownExtractor()
	src := Source{Path: "ARCH.md", Content: content, Extension: ".md"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Should produce at least one draft from the ## Decision heading.
	if len(drafts) == 0 {
		t.Error("expected at least one draft from ## Decision heading")
	}
}

func TestMarkdownExtractor_InlinePattern(t *testing.T) {
	content := []byte("Pattern: Use dependency injection for all services.\n")
	e := NewMarkdownExtractor()
	src := Source{Path: "notes.txt", Content: content, Extension: ".txt"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) == 0 {
		t.Error("expected a draft from inline Pattern: marker")
	}
}
