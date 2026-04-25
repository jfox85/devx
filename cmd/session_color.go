package cmd

import (
	"fmt"
	"strings"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var sessionColorCmd = &cobra.Command{
	Use:   "color <session-name> <color>",
	Short: "Set the color indicator for a session",
	Long: fmt.Sprintf(`Set the color indicator for a session. Available colors: %s`,
		strings.Join(session.Palette, ", ")),
	Args: cobra.ExactArgs(2),
	RunE: runSessionColor,
}

func init() {
	sessionCmd.AddCommand(sessionColorCmd)
}

func runSessionColor(cmd *cobra.Command, args []string) error {
	sessionName := args[0]
	color := args[1]

	if !session.IsValidColor(color) {
		return fmt.Errorf("invalid color %q. Valid colors: %s", color, strings.Join(session.Palette, ", "))
	}

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	if _, exists := store.GetSession(sessionName); !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	if err := store.UpdateSession(sessionName, func(s *session.Session) {
		s.Color = color
	}); err != nil {
		return fmt.Errorf("failed to set color: %w", err)
	}

	fmt.Printf("Set color for session '%s' to '%s'\n", sessionName, color)
	notifySessionUpdated(sessionName)
	return nil
}
