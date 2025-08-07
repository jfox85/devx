package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the TUI application
func Run() error {
	p := tea.NewProgram(
		InitialModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("error running TUI: %w", err)
	}

	return nil
}

// RunIfNoArgs runs the TUI if no command line arguments are provided
func RunIfNoArgs() error {
	// Check if any arguments were provided (besides the program name)
	if len(os.Args) <= 1 {
		return Run()
	}
	return nil
}
