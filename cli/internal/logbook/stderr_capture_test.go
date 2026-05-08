package logbook_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/momhq/mom/cli/internal/herald"
	"github.com/momhq/mom/cli/internal/librarian"
	"github.com/momhq/mom/cli/internal/logbook"
	"github.com/momhq/mom/cli/internal/vault"
)

// captureStderr is the same pattern as herald's; duplicated per
// package so the helper stays test-local. See herald/stderr_capture_test.go.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// TestWorker_Subscribe_LogsToStderr_OnEmptySessionID locks the
// contract that empty SessionID produces a stderr log, not a silent
// drop. The original Logbook regression was silent loss of audit
// data; visibility of the gap is the actual fix.
func TestWorker_Subscribe_LogsToStderr_OnEmptySessionID(t *testing.T) {
	w, _ := openWorker(t)
	bus := herald.NewBus()
	defer w.Subscribe(bus, "op.broken")()

	captured := captureStderr(t, func() {
		bus.Publish(herald.Event{
			Type:    "op.broken",
			Payload: map[string]any{"foo": "bar"},
		})
	})

	if !strings.Contains(captured, "logbook: drop") {
		t.Errorf("missing logbook drop log in stderr:\n%s", captured)
	}
	if !strings.Contains(captured, "op.broken") {
		t.Errorf("missing event type in stderr:\n%s", captured)
	}
	if !strings.Contains(captured, "session_id") {
		t.Errorf("stderr should mention the reason (empty session_id):\n%s", captured)
	}
}

// TestWorker_Subscribe_LogsToStderr_OnPersistFailure locks the
// contract that a Librarian write failure inside the subscriber is
// reported to stderr, not swallowed. Forces failure by using a
// Librarian whose underlying vault was closed BEFORE the publish.
func TestWorker_Subscribe_LogsToStderr_OnPersistFailure(t *testing.T) {
	dir := t.TempDir()
	migs := append(librarian.Migrations(), logbook.Migrations()...)
	v, err := vault.Open(filepath.Join(dir, "mom.db"), migs)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	lib := librarian.New(v)
	w := logbook.New(lib)

	bus := herald.NewBus()
	defer w.Subscribe(bus, "op.x")()

	// Close the vault out from under the worker so the next persist
	// fails. This simulates "vault unavailable" without manufacturing
	// a fault-injection path.
	if err := v.Close(); err != nil {
		t.Fatalf("v.Close: %v", err)
	}

	captured := captureStderr(t, func() {
		bus.Publish(herald.Event{Type: "op.x", SessionID: "s"})
	})

	if !strings.Contains(captured, "logbook: persist") {
		t.Errorf("missing persist-failure log in stderr:\n%s", captured)
	}
	if !strings.Contains(captured, "op.x") {
		t.Errorf("missing event type in stderr:\n%s", captured)
	}
}

// TestWorker_Log_DirectErrorReturn ensures Worker.Log itself still
// returns the error to direct callers — only the Subscribe path
// converts errors to stderr (because the publisher has no return to
// surface them on). Direct callers must still be able to handle
// errors.Is(err, librarian.ErrEmptyArg) etc.
func TestWorker_Log_DirectErrorReturn(t *testing.T) {
	w, _ := openWorker(t)
	err := w.Log("", "s", nil)
	if !errors.Is(err, librarian.ErrEmptyArg) {
		t.Fatalf("Worker.Log direct call: err = %v, want ErrEmptyArg", err)
	}
}
