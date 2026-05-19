package project_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/momhq/mom/cli/internal/project"
)

// writeBindFile writes a minimal .mom-project.yaml in dir with the given id.
func writeBindFile(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := []byte("version: \"1\"\nid: " + id + "\n")
	if err := os.WriteFile(filepath.Join(dir, ".mom-project.yaml"), body, 0o644); err != nil {
		t.Fatalf("write bind file: %v", err)
	}
}

// Tracer bullet: ResolveProject finds .mom-project.yaml in the cwd itself.
func TestResolveProject_FindsInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	writeBindFile(t, dir, "alpha")

	id, sourceFile, found, err := project.ResolveProject(dir)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true with bind file present")
	}
	if id != "alpha" {
		t.Errorf("id = %q, want alpha", id)
	}
	if !filepath.IsAbs(sourceFile) {
		t.Errorf("sourceFile must be absolute, got %q", sourceFile)
	}
	if filepath.Base(sourceFile) != ".mom-project.yaml" {
		t.Errorf("sourceFile basename = %q, want .mom-project.yaml", filepath.Base(sourceFile))
	}
}

// Walks up from a subdirectory to find the bind file in an ancestor.
func TestResolveProject_WalksUpFromSubdir(t *testing.T) {
	root := t.TempDir()
	writeBindFile(t, root, "outer")
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	id, _, found, err := project.ResolveProject(sub)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if !found || id != "outer" {
		t.Errorf("ResolveProject(subdir) = (%q, %v), want (outer, true)", id, found)
	}
}

// When ancestor and descendant both have bind files, the longest
// (nearest) match wins (ADR 0016).
func TestResolveProject_LongestAncestorWins(t *testing.T) {
	root := t.TempDir()
	writeBindFile(t, root, "outer")
	inner := filepath.Join(root, "apps", "web")
	writeBindFile(t, inner, "inner")
	deep := filepath.Join(inner, "src", "components")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	id, _, _, err := project.ResolveProject(deep)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if id != "inner" {
		t.Errorf("longest ancestor should win: id = %q, want inner", id)
	}
}

// No bind file anywhere on the path → not-found, no error.
func TestResolveProject_NoFileReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	id, sourceFile, found, err := project.ResolveProject(dir)
	if err != nil {
		t.Fatalf("ResolveProject must not error when no file exists, got: %v", err)
	}
	if found || id != "" || sourceFile != "" {
		t.Errorf("expected zero-result, got id=%q file=%q found=%v", id, sourceFile, found)
	}
}

// Malformed YAML returns an error (not a silent success).
func TestResolveProject_RejectsMalformedYaml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mom-project.yaml"),
		[]byte("not: valid: yaml: :"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := project.ResolveProject(dir)
	if err == nil {
		t.Errorf("expected error for malformed YAML")
	}
}

// Lax id validation — pathological values rejected, everything else accepted.
func TestResolveProject_IdValidation(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid lowercase", "foo", false},
		{"valid with dash", "pi-agents-cli", false},
		{"mixed case allowed", "MyService", false},
		{"spaces allowed", "my service", false},
		{"emoji allowed", "service 🚀", false},
		{"empty rejected", "", true},
		{"whitespace-only rejected", "   ", true},
		{"null byte rejected", "foo\x00bar", true},
		// Newline rejection is defense-in-depth — YAML normalises
		// embedded newlines in regular quoted strings to spaces, so
		// producing one without contrived block-scalar fixtures is
		// impractical. validateId still rejects them if they arrive.
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			body := "version: \"1\"\nid: \"" + c.id + "\"\n"
			if err := os.WriteFile(filepath.Join(dir, ".mom-project.yaml"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, _, err := project.ResolveProject(dir)
			if c.wantErr && err == nil {
				t.Errorf("expected error for id %q, got nil", c.id)
			}
			if !c.wantErr && err != nil {
				t.Errorf("expected no error for id %q, got %v", c.id, err)
			}
		})
	}
}
