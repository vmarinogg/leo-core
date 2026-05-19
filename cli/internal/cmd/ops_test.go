package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/momhq/mom/cli/internal/config"
)

// setupTestMemoryWithConfig creates a .mom/ with config.yaml and returns the temp dir.
func setupTestMemoryWithConfig(t *testing.T, harness string) string {
	t.Helper()
	dir := setupTestMemory(t) // reuse existing helper from memory_test.go (formerly kb_test.go)

	momDir := filepath.Join(dir, ".mom")

	// Write a real config.yaml.
	cfg := config.Default()
	// Default() already includes claude; if a different harness is requested,
	// add it (for test flexibility).
	if harness != "claude" {
		cfg.Harnesses[harness] = config.HarnessConfig{Enabled: true}
	}
	if err := config.Save(momDir, &cfg); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	// Create profiles dir with a valid profile.
	profilesDir := filepath.Join(momDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0755); err != nil {
		t.Fatalf("creating profiles dir: %v", err)
	}
	profileData := []byte("name: Generalist\ndescription: test\n")
	os.WriteFile(filepath.Join(profilesDir, "generalist.yaml"), profileData, 0644)

	return dir
}

// ── mom status tests ─────────────────────────────────────────────────────────

func TestStatusCmd_ShowsCentralShape(t *testing.T) {
	lib := openCentralTestLib(t)
	insertCentralTestMemory(t, lib, "ops status memory", "status check")

	buf := new(bytes.Buffer)
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	if err := runStatus(statusCmd, nil); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"MOM", "cwd", "vault", "memories", "types", "landmarks", "op events", "recording", "watcher"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"constraints", "skills"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("did not expect %q in output, got:\n%s", forbidden, out)
		}
	}
}

// TestHelperYamlParsesBadYaml confirms that {unclosed fails yaml.Unmarshal.
func TestHelperYamlParsesBadYaml(t *testing.T) {
	var v map[string]any
	err := yaml.Unmarshal([]byte("{unclosed\n"), &v)
	if err == nil {
		t.Fatal("expected yaml.Unmarshal to fail for '{unclosed' input")
	}
}
