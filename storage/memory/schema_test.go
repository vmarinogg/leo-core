package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validDoc() *Doc {
	return &Doc{
		ID:        "test-doc",
		Scope:     "project",
		Tags:      []string{"test"},
		Created:   time.Now().UTC(),
		CreatedBy: "owner",
		Content:   map[string]any{"fact": "a fact"},
	}
}

func TestValidate_ValidDoc(t *testing.T) {
	doc := validDoc()
	if err := doc.Validate(); err != nil {
		t.Fatalf("expected valid doc, got: %v", err)
	}
}

func TestValidate_InvalidID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"spaces", "has spaces"},
		{"empty", ""},
		{"spaces only", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validDoc()
			doc.ID = tt.id
			if err := doc.Validate(); err == nil {
				t.Errorf("expected error for id %q, got nil", tt.id)
			}
		})
	}
}

func TestValidate_SanitizedIDs(t *testing.T) {
	tests := []struct {
		name, input, expected string
	}{
		{"uppercase", "InvalidID", "invalidid"},
		{"underscores", "has_underscores", "has-underscores"},
		{"starts with hyphen", "-starts-bad", "starts-bad"},
		{"ends with hyphen", "ends-bad-", "ends-bad"},
		{"double hyphen", "double--hyphen", "double-hyphen"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validDoc()
			doc.ID = tt.input
			if err := doc.Validate(); err != nil {
				t.Errorf("expected sanitization to fix id %q, got: %v", tt.input, err)
			}
			if doc.ID != tt.expected {
				t.Errorf("expected id %q after sanitization, got %q", tt.expected, doc.ID)
			}
		})
	}
}

func TestValidate_ValidIDs(t *testing.T) {
	tests := []string{"simple", "kebab-case", "multi-word-id", "has-123-numbers"}
	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			doc := validDoc()
			doc.ID = id
			if err := doc.Validate(); err != nil {
				t.Errorf("expected valid for id %q, got: %v", id, err)
			}
		})
	}
}

func TestValidate_SummaryField(t *testing.T) {
	doc := validDoc()
	doc.Summary = "A concise one-line description"
	if err := doc.Validate(); err != nil {
		t.Fatalf("expected valid doc with summary, got: %v", err)
	}
	if doc.Summary != "A concise one-line description" {
		t.Errorf("expected summary %q, got %q", "A concise one-line description", doc.Summary)
	}
}

func TestValidate_InvalidScope(t *testing.T) {
	doc := validDoc()
	doc.Scope = "global"
	if err := doc.Validate(); err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestValidate_EmptyTags(t *testing.T) {
	doc := validDoc()
	doc.Tags = []string{}
	if err := doc.Validate(); err == nil {
		t.Fatal("expected error for empty tags")
	}
}

func TestValidate_InvalidTagFormat(t *testing.T) {
	doc := validDoc()
	doc.Tags = []string{"valid-tag", "   "}
	if err := doc.Validate(); err == nil {
		t.Fatal("expected error for invalid tag format")
	}
}

func TestValidate_SanitizedTags(t *testing.T) {
	doc := validDoc()
	doc.Tags = []string{"UPPER", "has_underscores", "double--dash", "pkg--websocket"}
	if err := doc.Validate(); err != nil {
		t.Fatalf("expected sanitization to fix tags, got: %v", err)
	}
	expected := []string{"upper", "has-underscores", "double-dash", "pkg-websocket"}
	for i, tag := range doc.Tags {
		if tag != expected[i] {
			t.Errorf("tag[%d]: expected %q, got %q", i, expected[i], tag)
		}
	}
}

func TestValidate_EmptyCreatedBy(t *testing.T) {
	doc := validDoc()
	doc.CreatedBy = ""
	if err := doc.Validate(); err == nil {
		t.Fatal("expected error for empty created_by")
	}
}

func TestValidate_NilContent(t *testing.T) {
	doc := validDoc()
	doc.Content = nil
	if err := doc.Validate(); err == nil {
		t.Fatal("expected error for nil content")
	}
}

func TestLoadDoc_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	os.WriteFile(path, []byte(`{
		"id": "test-doc",
		"scope": "project",
		"tags": ["test"],
		"created": "2026-04-13T00:00:00Z",
		"created_by": "owner",
		"content": {"fact": "test"}
	}`), 0644)

	doc, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("LoadDoc failed: %v", err)
	}
	if doc.ID != "test-doc" {
		t.Errorf("expected id %q, got %q", "test-doc", doc.ID)
	}
}

func TestLoadDoc_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{not json}`), 0644)

	if _, err := LoadDoc(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadDoc_FileNotFound(t *testing.T) {
	if _, err := LoadDoc("/nonexistent/path.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSaveDoc_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roundtrip.json")

	original := validDoc()
	if err := SaveDoc(path, original); err != nil {
		t.Fatalf("SaveDoc failed: %v", err)
	}

	loaded, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("LoadDoc failed: %v", err)
	}

	if loaded.ID != original.ID {
		t.Errorf("ID mismatch: %q vs %q", original.ID, loaded.ID)
	}
	if len(loaded.Tags) != len(original.Tags) {
		t.Errorf("Tags length mismatch: %d vs %d", len(original.Tags), len(loaded.Tags))
	}
}
