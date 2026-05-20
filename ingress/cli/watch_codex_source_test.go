package cli

import (
	"testing"

	"github.com/momhq/mom/shared/config"
)

// When the codex harness is enabled, buildWatcherSources must return a
// watcher.Source for it, wired to the CodexAdapter and using the harness's
// DefaultTranscriptDir.
func TestBuildWatcherSources_IncludesCodexWhenEnabled(t *testing.T) {
	t.Setenv("CODEX_HOME", "")

	cfg := config.Default()
	cfg.Harnesses["codex"] = config.HarnessConfig{Enabled: true}

	sources := buildWatcherSources(&cfg, "/tmp/project")

	var got *struct {
		dir string
		adp string
	}
	for _, s := range sources {
		if s.Harness == "codex" {
			got = &struct {
				dir string
				adp string
			}{s.TranscriptDir, s.Adapter.Name()}
		}
	}
	if got == nil {
		t.Fatalf("expected a codex source in buildWatcherSources, got %d sources (none codex)", len(sources))
	}
	if got.dir != "~/.codex/sessions" {
		t.Errorf("codex source TranscriptDir = %q, want ~/.codex/sessions", got.dir)
	}
	if got.adp != "codex" {
		t.Errorf("codex source Adapter.Name() = %q, want codex", got.adp)
	}
}

// CodexTranscriptDir config override is honored when set.
func TestBuildWatcherSources_CodexHonorsConfigOverride(t *testing.T) {
	cfg := config.Default()
	cfg.Harnesses["codex"] = config.HarnessConfig{Enabled: true}
	cfg.Watcher.CodexTranscriptDir = "/custom/sessions"

	sources := buildWatcherSources(&cfg, "/tmp/project")

	for _, s := range sources {
		if s.Harness == "codex" {
			if s.TranscriptDir != "/custom/sessions" {
				t.Errorf("override not honored: TranscriptDir = %q, want /custom/sessions", s.TranscriptDir)
			}
			return
		}
	}
	t.Fatalf("expected a codex source")
}
