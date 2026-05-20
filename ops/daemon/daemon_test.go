package daemon

import "testing"

func TestProjectHash_Deterministic(t *testing.T) {
	h1 := ProjectHash("/Users/test/project")
	h2 := ProjectHash("/Users/test/project")
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q != %q", h1, h2)
	}
}

func TestProjectHash_Unique(t *testing.T) {
	h1 := ProjectHash("/Users/test/project-a")
	h2 := ProjectHash("/Users/test/project-b")
	if h1 == h2 {
		t.Errorf("different paths produced same hash: %q", h1)
	}
}

func TestProjectHash_Length(t *testing.T) {
	h := ProjectHash("/Users/test/project")
	if len(h) != 12 {
		t.Errorf("expected 12 hex chars, got %d: %q", len(h), h)
	}
}
