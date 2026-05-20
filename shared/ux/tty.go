package ux

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// IsTTY returns true if w is connected to a terminal.
func IsTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(f.Fd())
	}
	return false
}
