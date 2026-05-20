package ux

import (
	"charm.land/lipgloss/v2"
)

// MOM brand palette.
var (
	Ink     = lipgloss.Color("#001423")
	Paper   = lipgloss.Color("#FFF5E5")
	Signal  = lipgloss.Color("#0066B1")
	Walnut  = lipgloss.Color("#3B1F0A")
	Archive = lipgloss.Color("#FFCC2C")
)

// Functional colors.
var (
	Success = lipgloss.Color("#608451")
	Error   = lipgloss.Color("#AE4C3B")
	Warning = lipgloss.Color("#EFDD6F")
	Muted   = lipgloss.Color("#6B7B8D")
)

// Text styles.
var (
	TextStyle    = lipgloss.NewStyle().Foreground(Paper)
	BoldStyle    = lipgloss.NewStyle().Foreground(Paper).Bold(true)
	MutedStyle   = lipgloss.NewStyle().Foreground(Muted)
	ArchiveStyle = lipgloss.NewStyle().Foreground(Archive)
	SuccessStyle = lipgloss.NewStyle().Foreground(Success)
	ErrorStyle   = lipgloss.NewStyle().Foreground(Error)
	WarningStyle = lipgloss.NewStyle().Foreground(Warning)
	SignalStyle  = lipgloss.NewStyle().Foreground(Signal)
)

// Symbols.
const (
	DiamondFilled = "◆"
	DiamondEmpty  = "◇"
	BulletFilled  = "●"
	BulletEmpty   = "○"
	Chevron       = "›"
	Checkmark     = "✔"
	Cross         = "✗"
	WarningSign   = "⚠"
)
