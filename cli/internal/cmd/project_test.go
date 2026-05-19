package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execProjectBind chdirs to dir, invokes `mom project bind --id <id>` via
// rootCmd, and returns combined output. Restores cwd via t.Cleanup.
func execProjectBind(t *testing.T, dir, id string, force bool) (string, error) {
	t.Helper()
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
