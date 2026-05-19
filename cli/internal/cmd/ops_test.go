package cmd

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
