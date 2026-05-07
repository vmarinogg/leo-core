package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	a, ok := r.Get("claude")
	if !ok {
		t.Fatal("expected to find claude adapter")
	}
	if a.Name() != "claude" {
		t.Errorf("expected name 'claude', got %q", a.Name())
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected false for unknown adapter")
	}
}

func TestRegistryDetectAll(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()

	os.MkdirAll(filepath.Join(home, ".claude"), 0755)

	r := NewRegistry(dir)
	detected := r.DetectAll()

	if len(detected) != 1 {
		t.Fatalf("expected 1 detected adapter (claude), got %d", len(detected))
	}

	names := make(map[string]bool)
	for _, a := range detected {
		names[a.Name()] = true
	}
	if !names["claude"] {
		t.Error("expected claude to be detected")
	}
}

func TestRegistryAll(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	all := r.All()
	if len(all) != 4 {
		t.Fatalf("expected 4 adapters, got %d", len(all))
	}

	names := make(map[string]bool)
	for _, a := range all {
		names[a.Name()] = true
	}
	for _, expected := range []string{"claude", "codex", "windsurf", "pi"} {
		if !names[expected] {
			t.Errorf("expected %q in All()", expected)
		}
	}
}
