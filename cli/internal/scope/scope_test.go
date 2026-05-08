package scope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/momhq/mom/cli/internal/scope"
)

// makeTree creates a directory tree under a temp dir and returns the temp root.
// dirs is a list of paths relative to the temp root that should be created.
func makeTree(t *testing.T, dirs ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatalf("makeTree: %v", err)
		}
	}
	return root
}

// writeConfig writes a minimal config.yaml with the given scope label.
func writeConfig(t *testing.T, leoDir, label string) {
	t.Helper()
	content := "version: \"1\"\nscope: " + label + "\nruntimes:\n  claude:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(leoDir, "config.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
}

func TestWalk_ThreeLevels(t *testing.T) {
	// Tree: root/.leo, root/a/.leo, root/a/b/.leo — cwd = root/a/b
	root := makeTree(t,
		".mom",
		"a/.mom",
		"a/b/.mom",
	)
	writeConfig(t, filepath.Join(root, ".mom"), "user")
	writeConfig(t, filepath.Join(root, "a", ".mom"), "org")
	writeConfig(t, filepath.Join(root, "a", "b", ".mom"), "repo")

	// Patch HOME to root so Walk stops there.
	t.Setenv("HOME", root)

	cwd := filepath.Join(root, "a", "b")
	scopes := scope.Walk(cwd)

	if len(scopes) != 3 {
		t.Fatalf("expected 3 scopes, got %d: %v", len(scopes), scopes)
	}

	// Nearest first.
	if scopes[0].Path != filepath.Join(root, "a", "b", ".mom") {
		t.Errorf("scopes[0].Path = %q, want %q", scopes[0].Path, filepath.Join(root, "a", "b", ".mom"))
	}
	if scopes[1].Path != filepath.Join(root, "a", ".mom") {
		t.Errorf("scopes[1].Path = %q", scopes[1].Path)
	}
	if scopes[2].Path != filepath.Join(root, ".mom") {
		t.Errorf("scopes[2].Path = %q", scopes[2].Path)
	}

	if scopes[0].Label != "repo" {
		t.Errorf("scopes[0].Label = %q, want repo", scopes[0].Label)
	}
	if scopes[1].Label != "org" {
		t.Errorf("scopes[1].Label = %q, want org", scopes[1].Label)
	}
	if scopes[2].Label != "user" {
		t.Errorf("scopes[2].Label = %q, want user", scopes[2].Label)
	}
}

func TestWalk_NoLeoDir(t *testing.T) {
	// cwd with no .mom/ anywhere.
	root := t.TempDir()
	t.Setenv("HOME", root)

	scopes := scope.Walk(root)
	if len(scopes) != 0 {
		t.Fatalf("expected 0 scopes, got %d", len(scopes))
	}
}

func TestWalk_StopsAtHome(t *testing.T) {
	// Tree: root/.leo, root/a/.leo — HOME = root/a (so root/.leo should not appear)
	root := makeTree(t,
		".mom",
		"a/.mom",
		"a/b",
	)
	writeConfig(t, filepath.Join(root, ".mom"), "user")
	writeConfig(t, filepath.Join(root, "a", ".mom"), "repo")

	t.Setenv("HOME", filepath.Join(root, "a"))

	cwd := filepath.Join(root, "a", "b")
	scopes := scope.Walk(cwd)

	// Should only find root/a/.leo (HOME boundary stops at root/a, inclusive).
	if len(scopes) != 1 {
		t.Fatalf("expected 1 scope, got %d: %v", len(scopes), scopes)
	}
	if scopes[0].Path != filepath.Join(root, "a", ".mom") {
		t.Errorf("scopes[0].Path = %q", scopes[0].Path)
	}
}

func TestWalk_NearestFirst(t *testing.T) {
	// Single .leo/ one level above cwd.
	root := makeTree(t, ".mom", "sub")
	writeConfig(t, filepath.Join(root, ".mom"), "repo")
	t.Setenv("HOME", root)

	scopes := scope.Walk(filepath.Join(root, "sub"))
	if len(scopes) != 1 {
		t.Fatalf("expected 1, got %d", len(scopes))
	}
}

func TestNearestWritable_Found(t *testing.T) {
	root := makeTree(t, ".mom")
	writeConfig(t, filepath.Join(root, ".mom"), "repo")
	t.Setenv("HOME", root)

	s, ok := scope.NearestWritable(root)
	if !ok {
		t.Fatal("expected NearestWritable to return ok=true")
	}
	if s.Path != filepath.Join(root, ".mom") {
		t.Errorf("Path = %q", s.Path)
	}
}

func TestNearestWritable_NotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)

	_, ok := scope.NearestWritable(root)
	if ok {
		t.Fatal("expected ok=false when no .mom/ exists")
	}
}

func TestDefaultScope_MissingField(t *testing.T) {
	// A .mom/ with no scope field in config.yaml outside $HOME defaults to "repo".
	root := makeTree(t, ".mom")
	content := "version: \"1\"\nruntimes:\n  claude:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(root, ".mom", "config.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	// HOME points elsewhere so the $HOME/.mom/ → "user" override does not trigger.
	t.Setenv("HOME", t.TempDir())

	scopes := scope.Walk(root)
	if len(scopes) != 1 {
		t.Fatalf("expected 1, got %d", len(scopes))
	}
	if scopes[0].Label != "repo" {
		t.Errorf("Label = %q, want repo", scopes[0].Label)
	}
}

func TestDefaultScope_MissingField_AtHome(t *testing.T) {
	// $HOME/.mom/ with no scope field defaults to "user" (override added in #219).
	root := makeTree(t, ".mom")
	content := "version: \"1\"\nruntimes:\n  claude:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(root, ".mom", "config.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", root)

	scopes := scope.Walk(root)
	if len(scopes) != 1 {
		t.Fatalf("expected 1, got %d", len(scopes))
	}
	if scopes[0].Label != "user" {
		t.Errorf("Label = %q, want user", scopes[0].Label)
	}
}

func TestMemoryCount(t *testing.T) {
	root := makeTree(t, ".mom/memory")
	writeConfig(t, filepath.Join(root, ".mom"), "repo")

	// Write 2 JSON files and 1 non-JSON.
	memDir := filepath.Join(root, ".mom", "memory")
	os.WriteFile(filepath.Join(memDir, "a.json"), []byte("{}"), 0644)    //nolint:errcheck
	os.WriteFile(filepath.Join(memDir, "b.json"), []byte("{}"), 0644)    //nolint:errcheck
	os.WriteFile(filepath.Join(memDir, "notes.txt"), []byte("hi"), 0644) //nolint:errcheck

	t.Setenv("HOME", root)
	scopes := scope.Walk(root)
	if len(scopes) == 0 {
		t.Fatal("no scopes found")
	}
	if scopes[0].MemoryCount() != 2 {
		t.Errorf("MemoryCount = %d, want 2", scopes[0].MemoryCount())
	}
}

func TestValidateLabel(t *testing.T) {
	valid := []string{"user", "org", "repo", "workspace", "custom", ""}
	for _, v := range valid {
		if err := scope.ValidateLabel(v); err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want nil", v, err)
		}
	}

	invalid := []string{"global", "admin", "team", "REPO"}
	for _, v := range invalid {
		if err := scope.ValidateLabel(v); err == nil {
			t.Errorf("ValidateLabel(%q) expected error, got nil", v)
		}
	}
}
