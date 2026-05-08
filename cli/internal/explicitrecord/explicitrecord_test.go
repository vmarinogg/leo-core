package explicitrecord

import (
	"errors"
	"testing"

	"github.com/momhq/mom/cli/internal/herald"
)

func TestLooksLikeRuntimeSessionID(t *testing.T) {
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
		if got := LooksLikeRuntimeSessionID(tc.id); got != tc.want {
			t.Fatalf("LooksLikeRuntimeSessionID(%q) = %v, want %v", tc.id, got, tc.want)
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
		t.Fatalf("got %q, want explicit runtime session", got)
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
