package cartographer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDependencyExtractor_Matches(t *testing.T) {
	e := NewDependencyManifestExtractor()

	tests := []struct {
		path string
		want bool
	}{
		{"package.json", true},
		{"go.mod", true},
		{"requirements.txt", true},
		{"Cargo.toml", true},
		{"pyproject.toml", true},
		{"main.go", false},
		{"README.md", false},
		{"some/nested/go.mod", true},
	}

	for _, tt := range tests {
		if got := e.Matches(tt.path); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestDependencyExtractor_PackageJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "package.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewDependencyManifestExtractor()
	src := Source{Path: "testdata/package.json", Content: data, Extension: ".json"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) < 4 { // react, axios, jest, typescript
		t.Errorf("expected >= 4 dependency drafts, got %d", len(drafts))
	}

	// Check specific packages are present.
	names := map[string]bool{}
	for _, d := range drafts {
		if n, ok := d.Content["package"].(string); ok {
			names[n] = true
		}
	}
	for _, expected := range []string{"react", "axios", "jest", "typescript"} {
		if !names[expected] {
			t.Errorf("expected dependency %q in drafts", expected)
		}
	}
}

func TestDependencyExtractor_GoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "go.mod"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewDependencyManifestExtractor()
	src := Source{Path: "testdata/go.mod", Content: data, Extension: ".mod"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Should have cobra and yaml.v3 (direct), not the indirect ones.
	if len(drafts) < 2 {
		t.Errorf("expected >= 2 direct dependency drafts, got %d", len(drafts))
	}

	names := map[string]bool{}
	for _, d := range drafts {
		if n, ok := d.Content["package"].(string); ok {
			names[n] = true
		}
	}
	if !names["github.com/spf13/cobra"] {
		t.Error("expected cobra in go.mod drafts")
	}
	if !names["gopkg.in/yaml.v3"] {
		t.Error("expected yaml.v3 in go.mod drafts")
	}
	// Indirect should be excluded.
	if names["github.com/inconshreveable/mousetrap"] {
		t.Error("indirect dep mousetrap should not appear in drafts")
	}
}

func TestDependencyExtractor_RequirementsTxt(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "requirements.txt"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewDependencyManifestExtractor()
	src := Source{Path: "testdata/requirements.txt", Content: data, Extension: ".txt"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) < 4 { // requests, numpy, pandas, pytest
		t.Errorf("expected >= 4 drafts, got %d", len(drafts))
	}

	names := map[string]bool{}
	for _, d := range drafts {
		if n, ok := d.Content["package"].(string); ok {
			names[n] = true
		}
	}
	for _, want := range []string{"requests", "numpy", "pandas", "pytest"} {
		if !names[want] {
			t.Errorf("expected %q in requirements drafts", want)
		}
	}
}

func TestDependencyExtractor_CargoToml(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "Cargo.toml"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewDependencyManifestExtractor()
	src := Source{Path: "testdata/Cargo.toml", Content: data, Extension: ".toml"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) < 3 { // serde, tokio, clap
		t.Errorf("expected >= 3 drafts, got %d", len(drafts))
	}

	names := map[string]bool{}
	for _, d := range drafts {
		if n, ok := d.Content["package"].(string); ok {
			names[n] = true
		}
	}
	if !names["serde"] {
		t.Error("expected serde in Cargo.toml drafts")
	}
}

func TestDependencyExtractor_PyprojectToml(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "pyproject.toml"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	e := NewDependencyManifestExtractor()
	src := Source{Path: "testdata/pyproject.toml", Content: data, Extension: ".toml"}

	drafts, err := e.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(drafts) < 2 { // fastapi, pydantic at minimum
		t.Errorf("expected >= 2 drafts from pyproject.toml, got %d", len(drafts))
	}
}
