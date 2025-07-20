package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var sessionAttachCmd = &cobra.Command{
	Use:   "attach <name>",
	Short: "Attach to an existing development session",
	Long:  `Attach to an existing development session, reopening editor and tmux.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionAttach,
}

func init() {
	sessionCmd.AddCommand(sessionAttachCmd)
}

func runSessionAttach(cmd *cobra.Command, args []string) error {
	name := args[0]
	
	// Load existing sessions
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	
	// Check if session exists
	sess, exists := store.GetSession(name)
	if !exists {
		return fmt.Errorf("session '%s' not found", name)
	}
	
	// Verify session path still exists
	if _, err := os.Stat(sess.Path); os.IsNotExist(err) {
		return fmt.Errorf("session path '%s' no longer exists", sess.Path)
	}
	
	fmt.Printf("Attaching to session '%s' at %s\n", name, sess.Path)
	
	// Clear attention flag since user is now looking at this session
	if sess.AttentionFlag {
		if err := session.ClearAttentionFlag(name); err != nil {
			fmt.Printf("Warning: Failed to clear attention flag: %v\n", err)
		} else {
			fmt.Printf("Cleared attention flag\n")
		}
	}
	
	// Launch editor (re-opens if closed)
	if err := session.AttachEditorToSession(name, sess.Path); err != nil {
		fmt.Printf("Warning: Failed to attach editor: %v\n", err)
	}
	
	// Check if the target tmux session exists
	if err := session.AttachTmuxSession(name); err != nil {
		// Session doesn't exist, try to launch it
		tmuxpPath := filepath.Join(sess.Path, ".tmuxp.yaml")
		if _, err := os.Stat(tmuxpPath); err == nil {
			fmt.Printf("Tmux session not found, launching new session...\n")
			if err := session.LaunchTmuxSession(sess.Path, name); err != nil {
				fmt.Printf("Warning: Failed to launch tmux session: %v\n", err)
				fmt.Printf("You can manually launch with: tmuxp load %s\n", tmuxpPath)
			}
		} else {
			fmt.Printf("Note: tmuxp config not found at %s\n", tmuxpPath)
		}
	} else {
		fmt.Printf("Attached to existing tmux session '%s'\n", name)
	}
	
	return nil
}