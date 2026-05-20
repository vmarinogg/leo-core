package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/ops/daemon"
	"github.com/momhq/mom/shared/pathutil"
)

// execProjectBind chdirs to dir, invokes `mom project bind --id <id>` via
// rootCmd, and returns combined output. Restores cwd via t.Cleanup.
//
// HOME and MOM_VAULT are isolated when the caller hasn't already set them,
// so tests cannot mutate the developer's real ~/.mom (#388: bind now
// touches the global watch registry when MOM is initialized).
func execProjectBind(t *testing.T, dir, id string, force bool) (string, error) {
	t.Helper()
	if os.Getenv("MOM_VAULT") == "" {
		isolated := t.TempDir()
		t.Setenv("HOME", isolated)
		t.Setenv("MOM_VAULT", filepath.Join(isolated, ".mom", "mom.db"))
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	args := []string{"project", "bind", "--id", id}
	if force {
		args = append(args, "--force")
	}
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

// Tracer: writing a binding creates .mom-project.yaml at cwd.
func TestProjectBind_WritesYamlAtCwd(t *testing.T) {
	dir := t.TempDir()
	out, err := execProjectBind(t, dir, "alpha", false)
	if err != nil {
		t.Fatalf("project bind: %v\noutput:\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".mom-project.yaml"))
	if err != nil {
		t.Fatalf("expected bind file to exist: %v", err)
	}
	if !strings.Contains(string(data), "id: alpha") {
		t.Errorf("expected `id: alpha` in file body, got:\n%s", data)
	}
}

// Cycle 2: bind file carries the watermark header so the user-owned
// distinction is obvious in-place (per ADR 0016 Q6).
func TestProjectBind_FileCarriesWatermark(t *testing.T) {
	dir := t.TempDir()
	if _, err := execProjectBind(t, dir, "alpha", false); err != nil {
		t.Fatalf("bind: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".mom-project.yaml"))
	if !strings.HasPrefix(string(data), "# MOM project binding") {
		t.Errorf("expected watermark header at top, got:\n%s", data)
	}
	if !strings.Contains(string(data), "version: \"1\"") {
		t.Errorf("expected version field, got:\n%s", data)
	}
}

// Cycle 3: pathological id is rejected (newline / null byte / empty).
func TestProjectBind_RejectsPathologicalId(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"null-byte", "foo\x00bar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			_, err := execProjectBind(t, dir, c.id, false)
			if err == nil {
				t.Errorf("expected error for id %q", c.id)
			}
			if _, err := os.Stat(filepath.Join(dir, ".mom-project.yaml")); err == nil {
				t.Errorf("bind file must not be written when id is rejected")
			}
		})
	}
}

// Cycle 4: refuses overwrite when an existing file declares a different
// id, unless --force is passed.
func TestProjectBind_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if _, err := execProjectBind(t, dir, "alpha", false); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	out, err := execProjectBind(t, dir, "beta", false)
	if err == nil {
		t.Fatalf("expected error overwriting alpha → beta without --force; output:\n%s", out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".mom-project.yaml"))
	if !strings.Contains(string(data), "id: alpha") {
		t.Errorf("original alpha binding must survive failed overwrite, got:\n%s", data)
	}
}

// Cycle 5: --force overwrites.
func TestProjectBind_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if _, err := execProjectBind(t, dir, "alpha", false); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if _, err := execProjectBind(t, dir, "beta", true); err != nil {
		t.Fatalf("force bind: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".mom-project.yaml"))
	if !strings.Contains(string(data), "id: beta") {
		t.Errorf("expected force overwrite to set id: beta, got:\n%s", data)
	}
}

// Per #388 (and ADR 0016): binding a directory must also register it
// with the global watch daemon. Without this, the daemon's registry
// stays empty after bind, no project-scoped watcher is started for
// the bound dir, and captured turns end up unscoped. The registry
// entry is the user-intent signal — bind is when intent is expressed.
func TestProjectBind_RegistersProjectWithDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, ".mom", "central.db"))

	// Minimal MOM-initialized state: central config.yaml present so
	// the bind command can resolve momDir and load enabled harnesses.
	centralDir := filepath.Join(home, ".mom")
	if err := os.MkdirAll(centralDir, 0o755); err != nil {
		t.Fatalf("mkdir centralDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(centralDir, "config.yaml"),
		[]byte("version: \"1\"\nharnesses:\n  claude:\n    enabled: true\n"), 0o644); err != nil {
		t.Fatalf("write central config: %v", err)
	}

	projectDir := t.TempDir()
	if _, err := execProjectBind(t, projectDir, "alpha", false); err != nil {
		t.Fatalf("project bind: %v", err)
	}

	reg, err := daemon.LoadRegistry()
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	key := pathutil.CanonicalDir(projectDir)
	entry, ok := reg[key]
	if !ok {
		t.Fatalf("registry missing entry for bound dir %q; got %d entries: %+v", key, len(reg), reg)
	}
	if entry.MomDir == "" {
		t.Errorf("registry entry has empty MomDir; want central momDir")
	}
	if len(entry.Harnesses) == 0 {
		t.Errorf("registry entry has no harnesses; want at least claude")
	}
}

// Per #388: when MOM is not yet initialized at bind time (e.g. a repo
// being prepared for a first-time MOM user, or the binding file written
// before `mom init`), bind must still succeed and leave the central
// vault untouched. The .mom-project.yaml is checked into VCS and is
// useful on its own; `mom init`/`mom upgrade` will register later.
func TestProjectBind_NoCrashWhenMomNotInitialized(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MOM_VAULT", filepath.Join(home, ".mom", "mom.db"))
	// Intentionally do NOT create centralDir/config.yaml.

	projectDir := t.TempDir()
	if _, err := execProjectBind(t, projectDir, "alpha", false); err != nil {
		t.Fatalf("project bind: %v", err)
	}
	// Binding file is still present.
	if _, err := os.Stat(filepath.Join(projectDir, ".mom-project.yaml")); err != nil {
		t.Errorf("binding file should be written even when MOM is uninitialized: %v", err)
	}
	// Registry file must not exist (no central config → no momDir → no register).
	if _, err := os.Stat(filepath.Join(home, ".mom", "watch-registry.json")); err == nil {
		t.Errorf("watch registry must not be written when MOM is uninitialized")
	}
}

// Sanity: re-binding with the SAME id is a no-op success (idempotent).
func TestProjectBind_SameIdIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := execProjectBind(t, dir, "alpha", false); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if _, err := execProjectBind(t, dir, "alpha", false); err != nil {
		t.Errorf("re-binding to same id should succeed without --force, got: %v", err)
	}
}
