package harness

import "testing"

func TestAdapterTiers(t *testing.T) {
	cases := []struct {
		name    string
		adapter Adapter
		want    Tier
	}{
		{"pi", NewPiAdapter(""), Native},
		{"claude", NewClaudeAdapter(""), Fluent},
		{"codex", NewCodexAdapter(""), Fluent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.adapter.Tier(); got != c.want {
				t.Errorf("%s.Tier() = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestTierString(t *testing.T) {
	cases := []struct {
		tier Tier
		want string
	}{
		{Native, "native"},
		{Fluent, "fluent"},
		{Functional, "functional"},
		{Tier(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.tier.String(); got != c.want {
			t.Errorf("Tier(%d).String() = %q, want %q", c.tier, got, c.want)
		}
	}
}
