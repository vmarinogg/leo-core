package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/momhq/mom/cli/internal/pathutil"
)

func TestRegistryRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	reg := Registry{
		"/home/user/project-a": RegistryEntry{
			MomDir:    "/home/user/project-a/.mom",
			Harnesses: []string{"claude"},
		},
		"/home/user/project-b": RegistryEntry{
			MomDir:    "/home/user/project-b/.mom",
			Harnesses: []string{"claude", "windsurf"},
		},
	}

	if err := SaveRegistry(reg); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}

	loaded, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
	entryA := loaded["/home/user/project-a"]
	if entryA.MomDir != "/home/user/project-a/.mom" {
		t.Errorf("unexpected momDir: %q", entryA.MomDir)
	}
	if len(entryA.Harnesses) != 1 || entryA.Harnesses[0] != "claude" {
		t.Errorf("unexpected harnesses: %v", entryA.Harnesses)
	}
}

func TestLoadRegistry_PromotesLegacyRuntimesField(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	path, err := RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
  "/proj": {"momDir":"/proj/.mom", "runtimes":["claude"]}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if got := reg["/proj"].Harnesses; len(got) != 1 || got[0] != "claude" {
		t.Fatalf("Harnesses = %v, want legacy runtimes promoted", got)
	}
}

func TestLoadRegistry_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg) != 0 {
		t.Errorf("expected empty registry, got %d entries", len(reg))
	}
}

func TestIsRegistryEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	empty, err := IsRegistryEmpty()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !empty {
		t.Error("expected empty registry")
	}

	// Add an entry.
	if err := RegisterProject("/test/proj", "/test/proj/.mom", []string{"claude"}); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	empty, err = IsRegistryEmpty()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if empty {
		t.Error("expected non-empty registry")
	}
}

func TestRegisterUnregister(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := RegisterProject("/proj/a", "/proj/a/.mom", []string{"claude"}); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	if err := RegisterProject("/proj/b", "/proj/b/.mom", []string{"windsurf"}); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	reg, _ := LoadRegistry()
	if len(reg) != 2 {
		t.Fatalf("expected 2, got %d", len(reg))
	}

	// Unregister one.
	if err := UnregisterProject("/proj/a"); err != nil {
		t.Fatalf("UnregisterProject: %v", err)
	}

	reg, _ = LoadRegistry()
	if len(reg) != 1 {
		t.Fatalf("expected 1, got %d", len(reg))
	}
	if _, ok := reg["/proj/b"]; !ok {
		t.Error("expected /proj/b to remain")
	}
}

func TestLoadRegistryCanonicalizesSymlinkedProjectDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	realProjectDir := filepath.Join(t.TempDir(), "real", "project")
	if err := os.MkdirAll(realProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkProjectDir := filepath.Join(t.TempDir(), "link-project")
	if err := os.Symlink(realProjectDir, linkProjectDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	// Simulate an older registry entry written before canonicalization.
	if err := SaveRegistry(Registry{
		linkProjectDir: {MomDir: filepath.Join(linkProjectDir, ".mom"), Harnesses: []string{"claude"}},
	}); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}

	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	canonicalProjectDir := pathutil.CanonicalDir(realProjectDir)
	if _, ok := reg[canonicalProjectDir]; !ok {
		t.Fatalf("registry missing canonical key %q: %#v", canonicalProjectDir, reg)
	}
	if _, ok := reg[linkProjectDir]; ok {
		t.Fatalf("registry retained symlink key %q: %#v", linkProjectDir, reg)
	}
}

func TestRegisterUnregisterCanonicalizesSymlinkedProjectDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	realProjectDir := filepath.Join(t.TempDir(), "real", "project")
	if err := os.MkdirAll(realProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkProjectDir := filepath.Join(t.TempDir(), "link-project")
	if err := os.Symlink(realProjectDir, linkProjectDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if err := RegisterProject(linkProjectDir, filepath.Join(linkProjectDir, ".mom"), []string{"claude"}); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}
	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	canonicalProjectDir := pathutil.CanonicalDir(realProjectDir)
	if _, ok := reg[canonicalProjectDir]; !ok {
		t.Fatalf("registry missing canonical key %q: %#v", canonicalProjectDir, reg)
	}

	if err := UnregisterProject(linkProjectDir); err != nil {
		t.Fatalf("UnregisterProject: %v", err)
	}
	if empty, err := IsRegistryEmpty(); err != nil || !empty {
		t.Fatalf("registry empty = %v, err = %v", empty, err)
	}
}

func TestRegistryAtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	if err := RegisterProject("/proj", "/proj/.mom", []string{"claude"}); err != nil {
		t.Fatalf("RegisterProject: %v", err)
	}

	// Verify no .tmp file remains.
	regPath, _ := RegistryPath()
	tmpPath := regPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmp file should not remain: %v", err)
	}

	// Verify the file is valid JSON.
	data, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatalf("reading registry: %v", err)
	}
	if len(data) == 0 {
		t.Error("registry file is empty")
	}
}

func TestGlobalLogsDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	dir, err := GlobalLogsDir()
	if err != nil {
		t.Fatalf("GlobalLogsDir: %v", err)
	}

	expected := filepath.Join(tmp, ".mom", "logs")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestPruneInvalidRegistryRemovesStaleEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	validProject := filepath.Join(tmp, "valid")
	validMom := filepath.Join(tmp, ".mom")
	if err := os.MkdirAll(validProject, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(validMom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(validMom, "config.yaml"), []byte("version: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(validProject, ".mom-project.yaml"), []byte("version: \"1\"\nid: valid-project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	staleProject := filepath.Join(tmp, "stale")
	staleMom := filepath.Join(staleProject, ".mom")
	if err := os.MkdirAll(staleProject, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SaveRegistry(Registry{
		validProject:                  {MomDir: validMom, Harnesses: []string{"pi"}},
		staleProject:                  {MomDir: staleMom, Harnesses: []string{"pi"}},
		filepath.Join(tmp, "nohooks"): {MomDir: validMom, Harnesses: nil},
	}); err != nil {
		t.Fatalf("SaveRegistry: %v", err)
	}

	report, err := PruneInvalidRegistry()
	if err != nil {
		t.Fatalf("PruneInvalidRegistry: %v", err)
	}
	if len(report.Removed) != 2 {
		t.Fatalf("removed %d entries, want 2: %#v", len(report.Removed), report.Removed)
	}

	reg, err := LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(reg) != 1 {
		t.Fatalf("registry len = %d, want 1: %#v", len(reg), reg)
	}
	if _, ok := reg[pathutil.CanonicalDir(validProject)]; !ok {
		t.Fatalf("valid project pruned: %#v", reg)
	}
}
