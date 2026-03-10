package tui

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/viper"
)

// Run starts the TUI application
func Run() error {
	// Auto-start web daemon if configured
	if viper.GetBool("web_autostart") {
		if err := ensureWebDaemonRunning(); err != nil {
			// Non-fatal: just log and continue
			fmt.Printf("Warning: could not start web daemon: %v\n", err)
		}
	}

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
