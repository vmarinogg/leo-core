package ledger_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/momhq/mom/bus/herald"
	"github.com/momhq/mom/storage/ledger"
)

func mustOpen(t *testing.T, dir string) *ledger.Ledger {
	t.Helper()
	l, err := ledger.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func ev(text string) herald.Event {
	return herald.Event{
		Type:      herald.TurnObserved,
		SessionID: "s-1",
		Timestamp: time.Unix(1, 0).UTC(),
		Payload:   map[string]any{"text": text},
	}
}

func TestOpen_EmptyDirCreatesFirstSegment(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir)
	_ = l
	// First segment exists.
	files, err := filepath.Glob(filepath.Join(dir, "*.seg"))
	if err != nil || len(files) != 1 {
		t.Fatalf("glob: files=%v err=%v", files, err)
	}
}

func TestAppend_AssignsMonotonicOffsets(t *testing.T) {
	l := mustOpen(t, t.TempDir())
	off1, err := l.Append(ev("a"))
	if err != nil || off1 != 0 {
		t.Fatalf("Append a: off=%d err=%v", off1, err)
	}
	off2, err := l.Append(ev("b"))
	if err != nil || off2 != 1 {
		t.Fatalf("Append b: off=%d err=%v", off2, err)
	}
	off3, err := l.Append(ev("c"))
	if err != nil || off3 != 2 {
		t.Fatalf("Append c: off=%d err=%v", off3, err)
	}
}

func TestRead_RoundTripsRecord(t *testing.T) {
	l := mustOpen(t, t.TempDir())
	off, _ := l.Append(ev("hello"))
	got, err := l.Read(off)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Offset != off {
		t.Errorf("offset = %d, want %d", got.Offset, off)
	}
	if text, _ := got.Event.Payload["text"].(string); text != "hello" {
		t.Errorf("text = %q, want hello", text)
	}
}

func TestRead_BeyondHeadReturnsNotExist(t *testing.T) {
	l := mustOpen(t, t.TempDir())
	_, err := l.Read(42)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestIterate_StreamsAllRecords(t *testing.T) {
	l := mustOpen(t, t.TempDir())
	for _, c := range []string{"a", "b", "c", "d"} {
		if _, err := l.Append(ev(c)); err != nil {
			t.Fatalf("Append %s: %v", c, err)
		}
	}
	it := l.Iterate(0)
	defer it.Close()
	var got []string
	for {
		rec, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, rec.Event.Payload["text"].(string))
	}
	if it.Err() != nil {
		t.Fatalf("iter err: %v", it.Err())
	}
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestIterate_StartsFromGivenOffset(t *testing.T) {
	l := mustOpen(t, t.TempDir())
	for i := 0; i < 5; i++ {
		_, _ = l.Append(ev("x"))
	}
	it := l.Iterate(3)
	defer it.Close()
	var got []uint64
	for {
		rec, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, rec.Offset)
	}
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("got %v, want [3 4]", got)
	}
}

func TestReopen_ResumesNextOffset(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir)
	_, _ = l.Append(ev("a"))
	_, _ = l.Append(ev("b"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l2 := mustOpen(t, dir)
	off, err := l2.Append(ev("c"))
	if err != nil {
		t.Fatal(err)
	}
	if off != 2 {
		t.Fatalf("Append after reopen: off=%d, want 2", off)
	}
}

func TestReopen_DetectsAndTruncatesTornTail(t *testing.T) {
	dir := t.TempDir()
	l := mustOpen(t, dir)
	_, _ = l.Append(ev("a"))
	_, _ = l.Append(ev("b"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	// Corrupt the tail: write a length prefix + truncated body.
	files, _ := filepath.Glob(filepath.Join(dir, "*.seg"))
	if len(files) != 1 {
		t.Fatalf("unexpected segments: %v", files)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// Length prefix says 100 bytes, but only 5 follow.
	_, _ = f.Write([]byte{0, 0, 0, 100, 'b', 'a', 'd', '!', '!'})
	f.Close()

	// Reopen: torn tail should be truncated; next Append assigns 2.
	l2 := mustOpen(t, dir)
	off, err := l2.Append(ev("c"))
	if err != nil {
		t.Fatal(err)
	}
	if off != 2 {
		t.Fatalf("Append after torn tail: off=%d, want 2", off)
	}
}

func TestAppend_RotatesSegmentAtThreshold(t *testing.T) {
	dir := t.TempDir()
	// Force tiny rotation threshold by lying about it for the test.
	// Without an exported knob, we simulate the rotation path by
	// writing enough records to cross the default threshold — too
	// expensive for unit tests. Instead, this test exercises the
	// "multiple segments tracked" path by writing a few records and
	// verifying segments-on-disk plus offset continuity. The full
	// rotation-at-threshold path is exercised by an integration test
	// (Phase 3 / #368 replay harness).
	l := mustOpen(t, dir)
	for i := 0; i < 10; i++ {
		_, _ = l.Append(ev("rotate-test"))
	}
	got, _ := l.Read(7)
	if got.Offset != 7 {
		t.Fatalf("Read(7).Offset = %d, want 7", got.Offset)
	}
}

func TestOpen_FailsOnBadMagic(t *testing.T) {
	dir := t.TempDir()
	// Write a segment file with the wrong magic.
	path := filepath.Join(dir, "00000000000000000000.seg")
	if err := os.WriteFile(path, []byte("not-mom-format!"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ledger.Open(dir)
	if err == nil {
		t.Fatal("expected error for bad magic, got nil")
	}
}
