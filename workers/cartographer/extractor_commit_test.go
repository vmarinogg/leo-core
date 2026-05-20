package cartographer

import (
	"testing"
)

func TestParseCommitLog_Conventional(t *testing.T) {
	// Simulate git log output with record separator \x1e and field separator \x1f.
	output := "" +
		"abc123\x1ffeat(auth): add OAuth2 login support\x1fThis enables SSO with Google.\x1e" +
		"def456\x1ffix(db): prevent nil pointer dereference on empty results\x1f\x1e" +
		"ghi789\x1frefactor(cli): extract scope resolver into separate package\x1f\x1e" +
		"jkl012\x1fchore: update dependencies\x1f\x1e" +
		"mno345\x1fsome random commit without conventional prefix\x1f\x1e"

	drafts, err := parseCommitLog(output, "/repo")
	if err != nil {
		t.Fatalf("parseCommitLog: %v", err)
	}

	if len(drafts) != 4 {
		t.Errorf("expected 4 drafts (4 conventional commits), got %d", len(drafts))
	}

	// Non-conventional should be skipped.
	for _, d := range drafts {
		if sha, ok := d.Content["commit_sha"].(string); ok && sha == "mno345" {
			t.Error("non-conventional commit should not produce a draft")
		}
	}
}

func TestParseCommitLog_ScopeTag(t *testing.T) {
	output := "abc123\x1ffeat(auth-service): add token refresh\x1f\x1e"

	drafts, err := parseCommitLog(output, "/repo")
	if err != nil {
		t.Fatalf("parseCommitLog: %v", err)
	}

	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(drafts))
	}

	hasAuthService := false
	for _, tag := range drafts[0].Tags {
		if tag == "auth-service" {
			hasAuthService = true
			break
		}
	}
	if !hasAuthService {
		t.Errorf("expected auth-service tag from scoped commit, got %v", drafts[0].Tags)
	}
}

func TestParseCommitLog_Provenance(t *testing.T) {
	output := "deadbeef\x1ffeat: add initial scaffold\x1f\x1e"

	drafts, err := parseCommitLog(output, "/myrepo")
	if err != nil {
		t.Fatalf("parseCommitLog: %v", err)
	}

	if len(drafts) == 0 {
		t.Fatal("expected 1 draft")
	}

	d := drafts[0]
	if d.Provenance.CommitSHA != "deadbeef" {
		t.Errorf("provenance.CommitSHA = %q, want deadbeef", d.Provenance.CommitSHA)
	}
	if d.Provenance.TriggerEvent != TriggerEvent {
		t.Errorf("provenance.TriggerEvent = %q, want %q", d.Provenance.TriggerEvent, TriggerEvent)
	}
}

func TestParseCommitLog_Empty(t *testing.T) {
	drafts, err := parseCommitLog("", "/repo")
	if err != nil {
		t.Fatalf("parseCommitLog: %v", err)
	}
	if len(drafts) != 0 {
		t.Errorf("expected 0 drafts from empty log, got %d", len(drafts))
	}
}
