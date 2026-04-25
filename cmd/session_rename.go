package cmd

import (
	"fmt"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var clearDisplayNameFlag bool

var sessionRenameCmd = &cobra.Command{
	Use:   "rename <session-name> [display-name]",
	Short: "Set or clear the display name for a session",
	Long: `Set a display name for a session. The display name is shown in the TUI,
web interface, and CLI list instead of the internal session name.

Use --clear to remove the display name and revert to the internal name.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSessionRename,
}

func init() {
	sessionCmd.AddCommand(sessionRenameCmd)
	sessionRenameCmd.Flags().BoolVar(&clearDisplayNameFlag, "clear", false, "Clear the display name")
}

func runSessionRename(cmd *cobra.Command, args []string) error {
	sessionName := args[0]

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	if _, exists := store.GetSession(sessionName); !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	if clearDisplayNameFlag {
		if err := store.UpdateSession(sessionName, func(s *session.Session) {
			s.DisplayName = ""
		}); err != nil {
			return fmt.Errorf("failed to clear display name: %w", err)
		}
		fmt.Printf("Cleared display name for session '%s'\n", sessionName)
		notifySessionUpdated(sessionName)
		return nil
	}

	if len(args) < 2 {
		return fmt.Errorf("display name required (or use --clear to remove)")
	}

	displayName := args[1]
	if !session.IsValidDisplayName(displayName) {
		return fmt.Errorf("invalid display name (max %d characters, no control characters)", session.MaxDisplayNameLen)
	}

	if err := store.UpdateSession(sessionName, func(s *session.Session) {
		s.DisplayName = displayName
	}); err != nil {
		return fmt.Errorf("failed to set display name: %w", err)
	}

	fmt.Printf("Set display name for session '%s' to '%s'\n", sessionName, displayName)
	notifySessionUpdated(sessionName)
	return nil
}
