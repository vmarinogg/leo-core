package ux

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// Spinner is a TTY-aware braille spinner with Signal-colored frames.
// On non-TTY writers it prints the label once and "done" on finish.
type Spinner struct {
	w      io.Writer
	tty    bool
	frames []string
	idx    atomic.Int32
	text   atomic.Value
	done   chan struct{}
}

// NewSpinner creates a spinner bound to w.
func NewSpinner(w io.Writer) *Spinner {
	return &Spinner{
		w:      w,
		tty:    IsTTY(w),
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		done:   make(chan struct{}),
	}
}

// Start begins the spinner with an initial label.
func (s *Spinner) Start(label string) {
	s.text.Store(label)
	if !s.tty {
		fmt.Fprintf(s.w, "%s %s... ", DiamondFilled, label)
		return
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				i := int(s.idx.Add(1)) % len(s.frames)
				frame := SignalStyle.Render(s.frames[i])
				txt := TextStyle.Render(s.text.Load().(string) + "...")
				fmt.Fprintf(s.w, "\r%s %s   ", frame, txt)
			}
		}
	}()
}

// Update changes the spinner label mid-flight.
func (s *Spinner) Update(label string) {
	s.text.Store(label)
}

// Stop halts the spinner and prints the final label with "done" in Success.
func (s *Spinner) Stop() {
	label := s.text.Load().(string)
	if !s.tty {
		fmt.Fprintln(s.w, "done")
		return
	}
	close(s.done)
	diamond := SignalStyle.Render(DiamondFilled)
	txt := TextStyle.Render(label + "...")
	done := SuccessStyle.Render("done")
	fmt.Fprintf(s.w, "\r\033[K%s %s %s\n", diamond, txt, done)
}

// StopFail halts the spinner and prints the label with "failed" in Error.
func (s *Spinner) StopFail() {
	label := s.text.Load().(string)
	if !s.tty {
		fmt.Fprintln(s.w, "failed")
		return
	}
	close(s.done)
	diamond := ErrorStyle.Render(DiamondFilled)
	txt := TextStyle.Render(label + "...")
	fail := ErrorStyle.Render("failed")
	fmt.Fprintf(s.w, "\r\033[K%s %s %s\n", diamond, txt, fail)
}
