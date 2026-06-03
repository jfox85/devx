package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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

	// Record attach time and assign a numbered slot
	if err := store.RecordAttach(name); err != nil {
		fmt.Printf("Warning: Failed to record attach time: %v\n", err)
	}
	if _, err := store.AssignSlot(name); err != nil {
		fmt.Printf("Warning: Failed to assign slot: %v\n", err)
	}

	if sess.IsContainerized() {
		return attachContainerSession(name, sess)
	}
	return attachHostSession(name, sess)
}

func attachContainerSession(name string, sess *session.Session) error {
	// Check container is running
	if !target.IsDockerRunning(sess.Target) {
		return fmt.Errorf("container for session '%s' is not running. Remove and recreate the session", name)
	}

	// Check if tmux is running inside the container; if not, load it
	checkCmd := target.ExecInSession(sess.Target, []string{"tmux", "has-session", "-t", "=" + name}, false)
	if checkCmd.Run() != nil {
		fmt.Println("Tmux session not found in container, loading...")
		loadCmd := target.ExecInSession(sess.Target, []string{
			"tmuxp", "load", "-d", "/workspace/.tmuxp.yaml", "-s", name,
		}, false)
		if output, err := loadCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to load tmux inside container: %w\n%s", err, output)
		}
	}

	// Attach to tmux inside the container
	attachCmd := target.ExecInSession(sess.Target, []string{"tmux", "attach", "-t", "=" + name}, true)
	attachCmd.Stdin = os.Stdin
	attachCmd.Stdout = os.Stdout
	attachCmd.Stderr = os.Stderr
	if err := attachCmd.Run(); err != nil {
		return fmt.Errorf("failed to attach to tmux in container: %w", err)
	}
	return nil
}

func attachHostSession(name string, sess *session.Session) error {
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
