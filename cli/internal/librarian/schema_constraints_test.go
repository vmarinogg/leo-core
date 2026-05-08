package librarian_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/librarian"
)

// TestSchemaCHECK_RejectsInvalidType locks the ADR 0012 vocabulary at
// the schema level. Even if Librarian's Insert ever stops validating
// Type at the API layer, the schema CHECK is the second line of
// defense — typos and unknown values fail fast.
func TestSchemaCHECK_RejectsInvalidType(t *testing.T) {
	l := openLib(t)

	in := validInsert()
	in.Type = "fictional-type"
	_, err := l.Insert(in)
	if err == nil {
		t.Fatal("expected CHECK violation for invalid type, got nil")
	}
	if !strings.Contains(err.Error(), "CHECK") && !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("expected CHECK/constraint error, got: %v", err)
	}
}

func TestSchemaCHECK_AcceptsAllADR0012Types(t *testing.T) {
	for _, tt := range []string{"episodic", "semantic", "procedural", "untyped"} {
		t.Run(tt, func(t *testing.T) {
			l := openLib(t)
			in := validInsert()
			in.Type = tt
			if _, err := l.Insert(in); err != nil {
				t.Fatalf("type=%q rejected unexpectedly: %v", tt, err)
			}
		})
	}
}

// TestSchemaCHECK_RejectsInvalidPromotionState locks the ADR 0011
// vocabulary at the schema level. UpdateOperational doesn't validate
// the value at the API today, so the schema CHECK is the only
// enforcement — makes the contract honest.
func TestSchemaCHECK_RejectsInvalidPromotionState(t *testing.T) {
	l := openLib(t)
	id, err := l.Insert(validInsert())
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	bad := "archived" // valid v0.X word, NOT in v0.30 vocabulary
	err = l.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &bad})
	if err == nil {
		t.Fatal("expected CHECK violation for promotion_state=archived, got nil")
	}
	if !strings.Contains(err.Error(), "CHECK") && !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("expected CHECK/constraint error, got: %v", err)
	}
}

func TestSchemaCHECK_AcceptsADR0011PromotionStates(t *testing.T) {
	l := openLib(t)
	for _, ps := range []string{"draft", "curated"} {
		id, err := l.Insert(validInsert())
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
		state := ps
		if err := l.UpdateOperational(id, librarian.OperationalUpdate{PromotionState: &state}); err != nil {
			t.Errorf("promotion_state=%q rejected unexpectedly: %v", ps, err)
		}
	}
}

// TestDelete_CascadesEdges_ButLeavesTagAndEntityIntact locks the
// ON DELETE CASCADE behavior on memory_tags and memory_entities.
// Cartographer regen and Upgrade rollback rely on this. The cascade
// must NOT touch the tag or entity rows themselves — those stay for
// other memories that may still reference them.
func TestDelete_CascadesEdges_ButLeavesTagAndEntityIntact(t *testing.T) {
	l := openLib(t)

	// Two memories share the same tag and entity — proves edges
	// cascade per-memory, not by tag/entity-wide cleanup.
	a, _ := l.Insert(validInsert())
	b, _ := l.Insert(validInsert())
	tid, _ := l.UpsertTag("recall")
	eid, _ := l.UpsertEntity("user", "Alice")
	_ = l.LinkTag(a, tid)
	_ = l.LinkTag(b, tid)
	_ = l.LinkEntity(a, eid, "created_by")
	_ = l.LinkEntity(b, eid, "created_by")

	// Delete only memory A.
	if err := l.Delete(a); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// A's edges are gone; B's edges remain.
	gotByTag, _ := l.MemoriesByTag("recall")
	if len(gotByTag) != 1 || gotByTag[0] != b {
		t.Errorf("post-delete MemoriesByTag = %v, want [%q]", gotByTag, b)
	}
	gotByEntity, _ := l.MemoriesByEntity("user", "Alice")
	if len(gotByEntity) != 1 || gotByEntity[0] != b {
		t.Errorf("post-delete MemoriesByEntity = %v, want [%q]", gotByEntity, b)
	}

	// Memory A is gone.
	if _, err := l.Get(a); !errors.Is(err, librarian.ErrNotFound) {
		t.Errorf("Get(a) after delete: err = %v, want ErrNotFound", err)
	}
	// Memory B still readable.
	if _, err := l.Get(b); err != nil {
		t.Errorf("Get(b) after delete of a: %v", err)
	}
}

func TestDelete_RejectsEmptyID(t *testing.T) {
	l := openLib(t)
	if err := l.Delete(""); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestDelete_NotFoundForUnknownID(t *testing.T) {
	l := openLib(t)
	if err := l.Delete("nope"); !errors.Is(err, librarian.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
