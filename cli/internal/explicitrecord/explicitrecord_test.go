package explicitrecord

import (
	"errors"
	"testing"

	"github.com/momhq/mom/cli/internal/herald"
)

func TestLooksLikeHarnessSessionID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"11111111-1111-4111-8111-111111111111", true},
		{"2026-05-07T20-00-00-000Z_11111111-1111-4111-8111-111111111111", true},
		{"fresh-install-e2e", false},
		{"2026-05-07-fresh-project", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := LooksLikeHarnessSessionID(tc.id); got != tc.want {
			t.Fatalf("LooksLikeHarnessSessionID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestResolveSessionIDPrefersExplicit(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "env-session")
	got, err := ResolveSessionID(" 11111111-1111-4111-8111-111111111111 ")
	if err != nil {
		t.Fatalf("ResolveSessionID: %v", err)
	}
	if got != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("got %q, want explicit harness session", got)
	}
}

func TestResolveSessionIDUsesHarnessEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-session")
	got, err := ResolveSessionID("")
	if err != nil {
		t.Fatalf("ResolveSessionID: %v", err)
	}
	if got != "claude-session" {
		t.Fatalf("got %q, want claude-session", got)
	}
}

// TestResolveSessionIDUsesMomSessionIDEnv asserts the neutral
// MOM_SESSION_ID env var is recognised by the resolver. This lets any
// harness (including Pi) satisfy the CLI by exporting a single
// well-known name, without MOM needing a bespoke entry per harness.
func TestResolveSessionIDUsesMomSessionIDEnv(t *testing.T) {
	t.Setenv("MOM_SESSION_ID", "mom-neutral-session")
	got, err := ResolveSessionID("")
	if err != nil {
		t.Fatalf("ResolveSessionID: %v", err)
	}
	if got != "mom-neutral-session" {
		t.Fatalf("got %q, want mom-neutral-session", got)
	}
}

// TestResolveSessionIDPrefersMomSessionIDOverHarnessEnv asserts the
// neutral MOM_SESSION_ID takes precedence over harness-specific env
// vars when both are set. The neutral name is the future contract;
// harness-specific names are kept for backwards compatibility but lose
// the race when explicitly overridden.
func TestResolveSessionIDPrefersMomSessionIDOverHarnessEnv(t *testing.T) {
	t.Setenv("MOM_SESSION_ID", "neutral")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude")
	got, err := ResolveSessionID("")
	if err != nil {
		t.Fatalf("ResolveSessionID: %v", err)
	}
	if got != "neutral" {
		t.Fatalf("got %q, want neutral (MOM_SESSION_ID should win)", got)
	}
}

// TestResolveSessionIDIgnoresRetiredWindsurfEnv asserts that the
// retired Windsurf harness env var is no longer consulted. Windsurf
// support was retired in #342/#343; the env key was kept around as
// dead code until v0.40 cleanup. Setting it must not resolve a
// session.
func TestResolveSessionIDIgnoresRetiredWindsurfEnv(t *testing.T) {
	t.Setenv("WINDSURF_TRAJECTORY_ID", "windsurf-traj")
	_, err := ResolveSessionID("")
	if !errors.Is(err, ErrMissingSessionID) {
		t.Fatalf("err = %v, want ErrMissingSessionID (Windsurf env must be ignored)", err)
	}
}

func TestResolveSessionIDRejectsInventedExplicit(t *testing.T) {
	_, err := ResolveSessionID("fresh-install-e2e")
	if err == nil {
		t.Fatal("expected error for invented explicit session_id")
	}
}

func TestResolveSessionIDRejectsMissing(t *testing.T) {
	_, err := ResolveSessionID("")
	if !errors.Is(err, ErrMissingSessionID) {
		t.Fatalf("err = %v, want ErrMissingSessionID", err)
	}
}

func TestPublishNormalizesAndPublishesMemoryRecord(t *testing.T) {
	bus := herald.NewBus()
	var got herald.Event
	var count int
	bus.Subscribe(herald.MemoryRecord, func(e herald.Event) {
		got = e
		count++
	})

	res, err := Publish(bus, Request{
		SessionID: "11111111-1111-4111-8111-111111111111",
		Summary:   "summary",
		Content:   map[string]any{"text": "remember this"},
		Tags:      []string{"MCP", "v0.30"},
		Actor:     "pi",
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if got.Type != herald.MemoryRecord || got.SessionID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("event = %+v", got)
	}
	if _, dup := got.Payload["session_id"]; dup {
		t.Fatal("session_id must stay on the event envelope")
	}
	if res.SessionID != "11111111-1111-4111-8111-111111111111" || res.Actor != "pi" {
		t.Fatalf("result = %+v", res)
	}
	tags, _ := got.Payload["tags"].([]string)
	if len(tags) != 2 || tags[0] != "mcp" || tags[1] != "v0-30" {
		t.Fatalf("tags = %v", tags)
	}
}

func TestPublishRejectsMissingSessionWithoutPublishing(t *testing.T) {
	bus := herald.NewBus()
	var count int
	bus.Subscribe(herald.MemoryRecord, func(e herald.Event) { count++ })

	_, err := Publish(bus, Request{Content: map[string]any{"text": "x"}})
	if !errors.Is(err, ErrMissingSessionID) {
		t.Fatalf("err = %v, want ErrMissingSessionID", err)
	}
	if count != 0 {
		t.Fatalf("published %d events, want 0", count)
	}
}
