package librarian_test

import (
	"errors"
	"testing"

	"github.com/momhq/mom/cli/internal/librarian"
)

// ── tags ──────────────────────────────────────────────────────────────────────

func TestUpsertTag_Idempotent(t *testing.T) {
	l := openLib(t)
	a, err := l.UpsertTag("recall")
	if err != nil {
		t.Fatalf("UpsertTag a: %v", err)
	}
	b, err := l.UpsertTag("recall")
	if err != nil {
		t.Fatalf("UpsertTag b: %v", err)
	}
	if a != b {
		t.Fatalf("UpsertTag returned different IDs for same name: %q vs %q", a, b)
	}
}

func TestUpsertTag_RejectsEmpty(t *testing.T) {
	l := openLib(t)
	for _, name := range []string{"", "   ", "\t"} {
		if _, err := l.UpsertTag(name); !errors.Is(err, librarian.ErrEmptyArg) {
			t.Errorf("UpsertTag(%q): err = %v, want ErrEmptyArg", name, err)
		}
	}
}

func TestUpsertTag_CaseSensitive(t *testing.T) {
	l := openLib(t)
	a, _ := l.UpsertTag("mcp")
	b, _ := l.UpsertTag("MCP")
	if a == b {
		t.Fatal("UpsertTag treated mcp/MCP as the same tag — case sensitivity broken")
	}
}

func TestLinkTag_AndQueryByTag(t *testing.T) {
	l := openLib(t)
	mid, _ := l.Insert(validInsert())
	tid, _ := l.UpsertTag("recall")

	if err := l.LinkTag(mid, tid); err != nil {
		t.Fatalf("LinkTag: %v", err)
	}
	// Idempotent: re-linking is a no-op success.
	if err := l.LinkTag(mid, tid); err != nil {
		t.Fatalf("LinkTag (re-link): %v", err)
	}

	ids, err := l.MemoriesByTag("recall")
	if err != nil {
		t.Fatalf("MemoriesByTag: %v", err)
	}
	if len(ids) != 1 || ids[0] != mid {
		t.Fatalf("MemoriesByTag = %v, want [%q]", ids, mid)
	}
}

func TestMemoriesByTag_UnknownTagReturnsEmpty(t *testing.T) {
	l := openLib(t)
	ids, err := l.MemoriesByTag("nonexistent")
	if err != nil {
		t.Fatalf("MemoriesByTag: %v", err)
	}
	if ids == nil {
		t.Error("got nil, want non-nil empty slice")
	}
	if len(ids) != 0 {
		t.Errorf("len = %d, want 0", len(ids))
	}
}

func TestRenameTag_MutatesTagRowOnly(t *testing.T) {
	l := openLib(t)
	mid, _ := l.Insert(validInsert())
	tid, _ := l.UpsertTag("mcp")
	_ = l.LinkTag(mid, tid)

	if err := l.RenameTag("mcp", "MCP"); err != nil {
		t.Fatalf("RenameTag: %v", err)
	}

	// Lookup by new name finds the linked memory; old name returns
	// nothing.
	gotNew, _ := l.MemoriesByTag("MCP")
	if len(gotNew) != 1 || gotNew[0] != mid {
		t.Fatalf("after rename MemoriesByTag(MCP) = %v, want [%q]", gotNew, mid)
	}
	gotOld, _ := l.MemoriesByTag("mcp")
	if len(gotOld) != 0 {
		t.Fatalf("after rename MemoriesByTag(mcp) = %v, want []", gotOld)
	}

	// Memory itself is unmodified — substance fields untouched.
	got, _ := l.Get(mid)
	if got.Content != `{"text":"hello world"}` {
		t.Errorf("memory content was rewritten by RenameTag: %q", got.Content)
	}
}

func TestRenameTag_NotFoundReturnsErr(t *testing.T) {
	l := openLib(t)
	err := l.RenameTag("does-not-exist", "new")
	if !errors.Is(err, librarian.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMergeTags_RejectsSelfMerge(t *testing.T) {
	l := openLib(t)
	_, _ = l.UpsertTag("recall")
	err := l.MergeTags("recall", "recall")
	if !errors.Is(err, librarian.ErrSelfMerge) {
		t.Fatalf("err = %v, want ErrSelfMerge", err)
	}
}

func TestMergeTags_CaseSensitiveDoesNotSelfMerge(t *testing.T) {
	l := openLib(t)
	_, _ = l.UpsertTag("mcp")
	_, _ = l.UpsertTag("MCP")
	if err := l.MergeTags("mcp", "MCP"); err != nil {
		t.Fatalf("merge mcp → MCP: %v", err)
	}
}

func TestMergeTags_RepointsEdgesAndDeletesSource(t *testing.T) {
	l := openLib(t)
	mid, _ := l.Insert(validInsert())
	src, _ := l.UpsertTag("memos")
	tgt, _ := l.UpsertTag("memo")
	_ = l.LinkTag(mid, src)

	if err := l.MergeTags("memos", "memo"); err != nil {
		t.Fatalf("MergeTags: %v", err)
	}

	// Edge re-pointed: the memory is now under tgt.
	tgtIDs, _ := l.MemoriesByTag("memo")
	if len(tgtIDs) != 1 || tgtIDs[0] != mid {
		t.Fatalf("MemoriesByTag(memo) = %v, want [%q]", tgtIDs, mid)
	}
	// Source tag is gone.
	srcIDs, _ := l.MemoriesByTag("memos")
	if len(srcIDs) != 0 {
		t.Fatalf("MemoriesByTag(memos) = %v after merge, want []", srcIDs)
	}
	_, _, _ = src, tgt, mid
}

func TestMergeTags_CollapsesDuplicateEdgesWithoutError(t *testing.T) {
	// A memory linked to BOTH source and target before the merge must
	// not produce a primary-key violation; the duplicate is collapsed.
	l := openLib(t)
	mid, _ := l.Insert(validInsert())
	src, _ := l.UpsertTag("memos")
	tgt, _ := l.UpsertTag("memo")
	_ = l.LinkTag(mid, src)
	_ = l.LinkTag(mid, tgt)

	if err := l.MergeTags("memos", "memo"); err != nil {
		t.Fatalf("MergeTags collapsed-edges: %v", err)
	}
	got, _ := l.MemoriesByTag("memo")
	if len(got) != 1 {
		t.Fatalf("memory listed %d times after merge, want 1", len(got))
	}
}

func TestMergeTags_SourceUnknownReturnsNotFound(t *testing.T) {
	l := openLib(t)
	_, _ = l.UpsertTag("target")
	err := l.MergeTags("source-unknown", "target")
	if !errors.Is(err, librarian.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ── entities ──────────────────────────────────────────────────────────────────

func TestUpsertEntity_Idempotent(t *testing.T) {
	l := openLib(t)
	a, err := l.UpsertEntity("user", "Vinicius")
	if err != nil {
		t.Fatalf("UpsertEntity a: %v", err)
	}
	b, _ := l.UpsertEntity("user", "Vinicius")
	if a != b {
		t.Fatalf("UpsertEntity returned different IDs for same (type, name): %q vs %q", a, b)
	}
}

func TestUpsertEntity_RejectsEmptyType(t *testing.T) {
	l := openLib(t)
	if _, err := l.UpsertEntity("", "Someone"); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestUpsertEntity_RejectsEmptyDisplayName(t *testing.T) {
	l := openLib(t)
	if _, err := l.UpsertEntity("user", ""); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("err = %v, want ErrEmptyArg", err)
	}
}

func TestUpsertEntity_DistinctTypeOrName(t *testing.T) {
	l := openLib(t)
	a, _ := l.UpsertEntity("user", "Alice")
	b, _ := l.UpsertEntity("user", "Bob")
	c, _ := l.UpsertEntity("group", "Alice")
	if a == b || a == c || b == c {
		t.Errorf("expected three distinct ids; got %q %q %q", a, b, c)
	}
}

func TestLinkEntity_AndQueryByEntity(t *testing.T) {
	l := openLib(t)
	mid, _ := l.Insert(validInsert())
	eid, _ := l.UpsertEntity("user", "Alice")

	if err := l.LinkEntity(mid, eid, "created_by"); err != nil {
		t.Fatalf("LinkEntity: %v", err)
	}
	// Idempotent for the same triple.
	if err := l.LinkEntity(mid, eid, "created_by"); err != nil {
		t.Fatalf("LinkEntity (re-link): %v", err)
	}
	// Different relationship is a separate edge — should also succeed.
	if err := l.LinkEntity(mid, eid, "mentions"); err != nil {
		t.Fatalf("LinkEntity mentions: %v", err)
	}

	ids, err := l.MemoriesByEntity("user", "Alice")
	if err != nil {
		t.Fatalf("MemoriesByEntity: %v", err)
	}
	// Two edges (created_by + mentions) on the same memory must yield
	// ONE result, not two. The contract is "memories referencing this
	// entity," not "edges." Locked by SELECT DISTINCT in the query.
	if len(ids) != 1 || ids[0] != mid {
		t.Fatalf("MemoriesByEntity = %v, want exactly one entry [%q] (DISTINCT contract)", ids, mid)
	}
}

func TestLinkEntity_RejectsEmptyArgs(t *testing.T) {
	l := openLib(t)
	if err := l.LinkEntity("", "e", "rel"); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Errorf("empty memoryID: err = %v", err)
	}
	if err := l.LinkEntity("m", "", "rel"); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Errorf("empty entityID: err = %v", err)
	}
	if err := l.LinkEntity("m", "e", ""); !errors.Is(err, librarian.ErrEmptyArg) {
		t.Errorf("empty relationship: err = %v", err)
	}
}
