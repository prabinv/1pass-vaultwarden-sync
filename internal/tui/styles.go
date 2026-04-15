package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorGreen  = lipgloss.Color("42")
	colorYellow = lipgloss.Color("214")
	colorRed    = lipgloss.Color("196")
	colorGray   = lipgloss.Color("240")
	colorWhite  = lipgloss.Color("255")
	colorBlue   = lipgloss.Color("69")

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			MarginBottom(1)

	styleCreate = lipgloss.NewStyle().Foreground(colorGreen)
	styleUpdate = lipgloss.NewStyle().Foreground(colorYellow)
	styleSkip   = lipgloss.NewStyle().Foreground(colorGray)
	styleError  = lipgloss.NewStyle().Foreground(colorRed)
	styleInfo   = lipgloss.NewStyle().Foreground(colorBlue)

	styleSummaryBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorGray).
			Padding(0, 1)

	styleCountdown = lipgloss.NewStyle().Foreground(colorGray).Italic(true)

	styleHelp = lipgloss.NewStyle().Foreground(colorGray).Faint(true)
)

// actionPrefix returns a styled prefix string for a list item action.
func actionPrefix(status itemStatus) string {
	switch status {
	case statusPending:
		return styleInfo.Render("  ·")
	case statusCreate:
		return styleCreate.Render(" [+]")
	case statusUpdate:
		return styleUpdate.Render(" [↑]")
	case statusDone:
		return styleCreate.Render(" [✓]")
	case statusError:
		return styleError.Render(" [✗]")
	default:
		return styleSkip.Render("  —")
	}
}
