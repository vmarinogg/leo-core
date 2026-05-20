package herald

import (
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// captureStderr redirects os.Stderr to a pipe for the duration of fn,
// then restores it and returns whatever was written. Used to lock the
// "logs to stderr" contract for handler-panic recovery.
//
// The redirect mutates a process-global, so this helper assumes the
// caller does not run in parallel with other code that writes to
// stderr. Tests using it should not call t.Parallel().
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

// TestPublish_HandlerPanic_LogsToStderrWithStack locks the contract
// that recovered panics are visible (line + stack), not just absorbed.
// Without this test, a future refactor that drops the log line would
// pass — TestPublish_HandlerPanicDoesNotBlockOthers only verifies
// fan-out, not visibility.
func TestPublish_HandlerPanic_LogsToStderrWithStack(t *testing.T) {
	bus := NewBus()
	var sentinel atomic.Int64
	bus.Subscribe(Error, func(e Event) { panic("specific-panic-marker-x42") })
	bus.Subscribe(Error, func(e Event) { sentinel.Add(1) })

	captured := captureStderr(t, func() {
		bus.Publish(Event{Type: Error, SessionID: "s"})
	})

	// Sibling handler still fires.
	if got := sentinel.Load(); got != 1 {
		t.Errorf("sibling handler fired %d times, want 1", got)
	}

	// Stderr contains the panic value, the event type, and a stack frame.
	if !strings.Contains(captured, `herald: handler for "error" panicked`) {
		t.Errorf("missing panic prefix in stderr:\n%s", captured)
	}
	if !strings.Contains(captured, "specific-panic-marker-x42") {
		t.Errorf("missing panic value in stderr:\n%s", captured)
	}
	if !strings.Contains(captured, "goroutine ") {
		t.Errorf("missing goroutine stack in stderr:\n%s", captured)
	}
}

// TestPublish_NoPanic_NoStderr locks the inverse: a normal publish
// produces zero stderr output. Catches a future bug where a debug log
// is left in the hot path.
func TestPublish_NoPanic_NoStderr(t *testing.T) {
	bus := NewBus()
	bus.Subscribe(SessionStart, func(e Event) {})

	captured := captureStderr(t, func() {
		bus.Publish(Event{Type: SessionStart, SessionID: "s"})
	})

	if captured != "" {
		t.Errorf("expected no stderr output for normal publish, got:\n%s", captured)
	}
}
