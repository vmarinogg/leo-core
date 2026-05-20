package daemon_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/momhq/mom/ops/daemon"
)

// writeFakeBinary creates a stand-in binary file at the given path and
// returns the path. Content is non-empty so mtime/size are stable.
func writeFakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake-mom-binary"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	return path
}

// Tracer: record a sentinel for a binary, then matches reports true for
// that same binary.
func TestBinaryVersion_RecordAndMatch_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bin := writeFakeBinary(t, t.TempDir(), "mom")

	if err := daemon.RecordBinaryVersion(bin); err != nil {
		t.Fatalf("RecordBinaryVersion: %v", err)
	}
	match, err := daemon.BinaryVersionMatches(bin)
	if err != nil {
		t.Fatalf("BinaryVersionMatches: %v", err)
	}
	if !match {
		t.Errorf("expected match=true immediately after recording")
	}
}

// Cycle 2: when the binary at the recorded path is replaced (different
// content → new mtime), match reports false.
func TestBinaryVersion_DetectsMtimeChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binDir := t.TempDir()
	bin := writeFakeBinary(t, binDir, "mom")

	if err := daemon.RecordBinaryVersion(bin); err != nil {
		t.Fatalf("record: %v", err)
	}
	// Rewrite the binary with different content; mtime advances.
	if err := os.Chtimes(bin, time.Now(), time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	match, err := daemon.BinaryVersionMatches(bin)
	if err != nil {
		t.Fatalf("matches: %v", err)
	}
	if match {
		t.Errorf("expected mismatch after mtime change")
	}
}

// Cycle 3: when the resolved binary path differs (e.g. brew Cellar
// pointer swapped), match reports false.
func TestBinaryVersion_DetectsPathChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	binA := writeFakeBinary(t, t.TempDir(), "mom")
	binB := writeFakeBinary(t, t.TempDir(), "mom")

	if err := daemon.RecordBinaryVersion(binA); err != nil {
		t.Fatalf("record: %v", err)
	}
	match, err := daemon.BinaryVersionMatches(binB)
	if err != nil {
		t.Fatalf("matches: %v", err)
	}
	if match {
		t.Errorf("expected mismatch when querying a different binary path")
	}
}

// Cycle 4: when the sentinel does not exist, match reports false (no
// error — "no record → reinstall" is the safe default).
func TestBinaryVersion_MissingSentinelReportsMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := writeFakeBinary(t, t.TempDir(), "mom")

	match, err := daemon.BinaryVersionMatches(bin)
	if err != nil {
		t.Fatalf("matches must not error when sentinel is missing: %v", err)
	}
	if match {
		t.Errorf("expected match=false when no sentinel exists")
	}
}

// Cycle 5: a corrupted sentinel reports mismatch (no error — treat as
// "needs reinstall," same as missing).
func TestBinaryVersion_MalformedSentinelReportsMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := writeFakeBinary(t, t.TempDir(), "mom")

	// Write garbage to the expected sentinel path.
	sentinel, err := daemon.BinaryVersionSentinelPath()
	if err != nil {
		t.Fatalf("sentinel path: %v", err)
	}
	if err := os.WriteFile(sentinel, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt sentinel: %v", err)
	}

	match, err := daemon.BinaryVersionMatches(bin)
	if err != nil {
		t.Fatalf("matches must not error on malformed sentinel: %v", err)
	}
	if match {
		t.Errorf("expected match=false on malformed sentinel")
	}
}

// Cycle 6: simulates the brew failure mode. Real binary lives at
// Cellar/X.Y.Z/bin/mom and /opt/homebrew/bin/mom is a symlink. After
// `brew upgrade`, the symlink is re-pointed to a new Cellar dir. The
// sentinel records the resolved (Cellar) path at install time, so the
// re-pointed symlink registers as a mismatch — exactly what we want
// ensureGlobalDaemon to detect.
func TestBinaryVersion_DetectsBrewStyleSymlinkSwap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cellarA := writeFakeBinary(t, t.TempDir(), "mom") // pre-upgrade target
	cellarB := writeFakeBinary(t, t.TempDir(), "mom") // post-upgrade target
	link := filepath.Join(t.TempDir(), "mom")
	if err := os.Symlink(cellarA, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Record via the symlink — internally resolves to cellarA.
	if err := daemon.RecordBinaryVersion(link); err != nil {
		t.Fatalf("record: %v", err)
	}
	if match, _ := daemon.BinaryVersionMatches(link); !match {
		t.Fatalf("pre-swap should match")
	}

	// Brew upgrade: re-point the symlink at the new Cellar dir.
	if err := os.Remove(link); err != nil {
		t.Fatalf("rm link: %v", err)
	}
	if err := os.Symlink(cellarB, link); err != nil {
		t.Fatalf("relink: %v", err)
	}

	match, err := daemon.BinaryVersionMatches(link)
	if err != nil {
		t.Fatalf("matches: %v", err)
	}
	if match {
		t.Errorf("symlink swap (Cellar pointer change) should register as mismatch")
	}
}
