package tui

import "github.com/charmbracelet/lipgloss"

var (
	logoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true).
			Align(lipgloss.Center).
			MarginTop(1).
			MarginBottom(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")).
			MarginLeft(2).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("170")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true).
			MarginLeft(2)

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	previewStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(1, 2).
			MarginLeft(1)

	sessionListStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(1, 2)

	additionsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")) // Green

	deletionsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")) // Red
)

// SessionColorStyles maps session color names to lipgloss styles for the dot indicator.
var SessionColorStyles = map[string]lipgloss.Style{
	"red":    lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	"blue":   lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
	"green":  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
	"yellow": lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
	"purple": lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
	"orange": lipgloss.NewStyle().Foreground(lipgloss.Color("208")),
	"pink":   lipgloss.NewStyle().Foreground(lipgloss.Color("213")),
	"cyan":   lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
}
