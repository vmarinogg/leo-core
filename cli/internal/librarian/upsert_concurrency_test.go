package librarian_test

import (
	"sync"
	"testing"

	"github.com/momhq/mom/cli/internal/librarian"
)

// TestUpsertTag_ConcurrentSameNameProducesOneRow locks the lesson the
// previous attempt missed: lookup-then-insert was not atomic, so two
// concurrent UpsertTag("recall") calls both passed the SELECT, both
// INSERTed, and the second hit UNIQUE(name). Drafter will run this
// path under load when capture events arrive close together — the
// race is not theoretical.
//
// The fixed contract: N concurrent calls for the same name produce
// exactly one tag row, and every caller sees the same id.
func TestUpsertTag_ConcurrentSameNameProducesOneRow(t *testing.T) {
	l := openLib(t)

	const N = 50
	var wg sync.WaitGroup
	ids := make([]string, N)
	errs := make([]error, N)

	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			id, err := l.UpsertTag("recall")
			ids[i] = id
			errs[i] = err
		}()
	}
	wg.Wait()

	// No goroutine surfaced an error.
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: UpsertTag returned %v", i, err)
		}
	}

	// Every goroutine saw the same id.
	first := ids[0]
	if first == "" {
		t.Fatal("first goroutine got empty id")
	}
	for i := 1; i < N; i++ {
		if ids[i] != first {
			t.Errorf("goroutine %d saw id %q, want %q (race produced multiple tag rows)", i, ids[i], first)
		}
	}

	// Sanity: subsequent UpsertTag yields the same id (idempotent).
	again, err := l.UpsertTag("recall")
	if err != nil {
		t.Fatalf("post-race UpsertTag: %v", err)
	}
	if again != first {
		t.Errorf("post-race id = %q, want %q", again, first)
	}
}

func TestUpsertEntity_ConcurrentSamePairProducesOneRow(t *testing.T) {
	l := openLib(t)

	const N = 50
	var wg sync.WaitGroup
	ids := make([]string, N)
	errs := make([]error, N)

	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			id, err := l.UpsertEntity("user", "Alice")
			ids[i] = id
			errs[i] = err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: UpsertEntity returned %v", i, err)
		}
	}

	first := ids[0]
	if first == "" {
		t.Fatal("first goroutine got empty id")
	}
	for i := 1; i < N; i++ {
		if ids[i] != first {
			t.Errorf("goroutine %d saw id %q, want %q", i, ids[i], first)
		}
	}
}

// Idempotent + correct-result: existing tests in graph_test.go
// (TestUpsertTag_Idempotent, TestUpsertEntity_Idempotent) cover the
// single-caller case. This file adds the concurrent contract on top.
var _ = librarian.NormalizeTagName // keep the librarian import non-test-only
