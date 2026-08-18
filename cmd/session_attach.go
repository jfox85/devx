package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
	"github.com/spf13/cobra"
)

var sessionAttachCmd = &cobra.Command{
	Use:   "attach <name>",
	Short: "Attach to an existing development session",
	Long:  `Attach to an existing development session's tmux environment.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionAttach,
}

func init() {
	sessionCmd.AddCommand(sessionAttachCmd)
}

var ensureTmuxForAttach = target.EnsureTmuxSession
var attachReadyTmux = target.AttachReadyTmuxSession

// readyAttach records activity only after the target session is known to be
// ready, immediately before handing the terminal to the blocking attach path.
func readyAttach(store *session.SessionStore, name string, sess *session.Session) error {
	if err := ensureTmuxForAttach(name, sess); err != nil {
		return err
	}
	if _, err := store.MarkActive(name, time.Now()); err != nil {
		return fmt.Errorf("mark session active: %w", err)
	}
	return attachReadyTmux(name, sess)
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

	return readyAttach(store, name, sess)
}
