package ux

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// ThemeMOM returns a huh theme styled with the MOM brand palette.
func ThemeMOM(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)

	t.Focused.Base = t.Focused.Base.BorderForeground(Muted)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(Archive)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(Archive)
	t.Focused.Description = t.Focused.Description.Foreground(Muted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(Error)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(Error)
	t.Focused.Directory = t.Focused.Directory.Foreground(Signal)
	t.Focused.File = t.Focused.File.Foreground(Paper)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(Archive)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(Archive)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(Archive)
	t.Focused.Option = t.Focused.Option.Foreground(Paper)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(Archive)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(Signal)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(Signal)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(Paper)
	t.Focused.UnselectedPrefix = t.Focused.UnselectedPrefix.Foreground(Muted)
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(Paper).Background(Signal).Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(Paper).Background(Ink)

	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Archive)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(Muted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Archive)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}
