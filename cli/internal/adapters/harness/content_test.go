package harness

import (
	"strings"
	"testing"
)

// TestBuildMinimalContextContent verifies the slim MCP-first boot content.
func TestBuildMinimalContextContent(t *testing.T) {
	content := BuildMinimalContextContent()

	for _, want := range []string{"mom_status", "/mom-status", "/mom-recall", "/mom-wrap-up", "CLI", "MCP fallback"} {
		if !strings.Contains(content, want) {
			t.Errorf("minimal content must mention %q", want)
		}
	}

	words := strings.Fields(content)
	if len(words) >= 100 {
		t.Errorf("minimal content should be <100 words, got %d", len(words))
	}

	forbidden := []string{"## Voice", "## Constraints", "## Skills", "## During work", "mom" + "_" + "record", "recording", "install"}
	for _, f := range forbidden {
		if strings.Contains(content, f) {
			t.Errorf("minimal content must not contain %q", f)
		}
	}
}

// TestBuildContextContent_StillWorks verifies the legacy full-content function
// continues to produce expected output after the minimal variant was added.
func TestBuildContextContent_StillWorks(t *testing.T) {
	cfg := Config{
		Version: "1",
		User: UserConfig{
			Language:          "en",
			Autonomy:          "balanced",
			CommunicationMode: "concise",
		},
	}

	constraints := []Constraint{
		{ID: "anti-hallucination", Summary: "When unsure, say you don't know."},
	}
	skills := []Skill{
		{ID: "session-wrap-up", Summary: "End-of-session knowledge propagation."},
	}
	identity := &Identity{What: "MOM — a living knowledge infrastructure."}

	content := BuildContextContent(cfg, constraints, skills, identity)

	checks := []string{
		"MOM — Memory Oriented Machine",
		"## Constraints",
		"## Skills",
		"anti-hallucination",
		"session-wrap-up",
		"## Voice",
		"## Memory",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("BuildContextContent missing %q", check)
		}
	}
}
