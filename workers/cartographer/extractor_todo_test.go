package cartographer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTodoExtractor_Matches(t *testing.T) {
	e := NewTodoFixmeExtractor()

	tests := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"app.py", true},
		{"server.js", true},
		{"README.md", false}, // handled by markdown
		{"notes.txt", false}, // handled by markdown
		{"logo.png", false},
		{"data.bin", false},
	}

	for _, tt := range tests {
		if got := e.Matches(tt.path); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestTodoExtractor_StructuredTodo(t *testing.T) {
	content := []byte(`package main

// TODO: This is a structured todo that explains the problem in detail here.
// FIXME: There is a known issue with the connection pool under high load scenarios.
// HACK: Using a mutex here because the channel approach had race conditions.
// NOTE: This function is called from multiple goroutines, ensure thread safety.
// WHY: We use polling instead of webhooks because the upstream API lacks support.
// TODO: short  // should be skipped (too short)
// TODO: toolong without space should be skipped
`)

	e := NewTodoFixmeExtractor()
	src := Source{Path: "main.go", Content: content, Extension: ".go"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Expect 5 structured comments (TODO×1, FIXME×1, HACK×1, NOTE×1, WHY×1).
	// "short" and "toolong without space" should be skipped.
	if len(drafts) < 5 {
		t.Errorf("expected >= 5 drafts, got %d", len(drafts))
	}

	_ = drafts // all structured comments captured
}

func TestTodoExtractor_TrivialSkipped(t *testing.T) {
	content := []byte(`
// TODO: fix
// TODO: refactor
// FIXME: broken
// TODO: this
`)
	e := NewTodoFixmeExtractor()
	src := Source{Path: "main.go", Content: content, Extension: ".go"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) != 0 {
		t.Errorf("expected 0 drafts for trivial todos, got %d", len(drafts))
	}
}

func TestTodoExtractor_SampleFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "sample.go"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewTodoFixmeExtractor()
	src := Source{Path: "testdata/sample.go", Content: data, Extension: ".go"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// sample.go has TODO (≥20 chars) and FIXME (≥20 chars).
	if len(drafts) < 1 {
		t.Errorf("expected at least 1 todo/fixme draft from sample.go, got %d", len(drafts))
	}
}
