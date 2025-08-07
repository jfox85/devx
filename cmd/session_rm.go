package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	forceFlag bool
)

var sessionRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a development session",
	Long:  `Remove a development session, including worktree, routes, and metadata.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionRm,
}

func init() {
	sessionCmd.AddCommand(sessionRmCmd)
	sessionRmCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "Force removal without confirmation")
}

func runSessionRm(cmd *cobra.Command, args []string) error {
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

	// Confirm deletion unless force flag is used
	if !forceFlag {
		fmt.Printf("This will remove session '%s' and its worktree at %s\n", name, sess.Path)
		fmt.Print("Are you sure? (y/N): ")

		var response string
		fmt.Scanln(&response)

		if response != "y" && response != "Y" && response != "yes" && response != "Yes" {
			fmt.Println("Aborted")
			return nil
		}
	}

	// Terminate editor if it's running
	if err := session.TerminateEditor(name); err != nil {
		fmt.Printf("Warning: failed to terminate editor: %v\n", err)
	}

	// Kill tmux session if it exists
	if err := killTmuxSession(name); err != nil {
		fmt.Printf("Warning: failed to kill tmux session: %v\n", err)
	}

	// Remove Caddy routes
	if sess.Routes != nil && len(sess.Routes) > 0 {
		if err := caddy.DestroySessionRoutes(name, sess.Routes); err != nil {
			fmt.Printf("Warning: failed to remove Caddy routes: %v\n", err)
		}
	}

	// Run cleanup command if configured
	if err := session.RunCleanupCommandForShell(sess); err != nil {
		fmt.Printf("Warning: cleanup command failed: %v\n", err)
	}

	// Remove git worktree
	if err := removeGitWorktree(sess.Path); err != nil {
		fmt.Printf("Warning: failed to remove git worktree: %v\n", err)
	}

	// Remove session from metadata
	if err := store.RemoveSession(name); err != nil {
		return fmt.Errorf("failed to remove session metadata: %w", err)
	}

	fmt.Printf("Removed session '%s'\n", name)
	return nil
}

func killTmuxSession(sessionName string) error {
	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil // tmux not available, skip
	}

	// Try to kill the session
	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	err := cmd.Run()

	// Don't treat "session not found" as an error
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil // session doesn't exist, which is fine
		}
		return err
	}

	fmt.Printf("Killed tmux session '%s'\n", sessionName)
	return nil
}

func removeGitWorktree(worktreePath string) error {
	// Check if worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil // already removed
	}

	// Use git worktree remove command with --force flag as specified in requirements
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If git command fails, try manual removal
		if removeErr := os.RemoveAll(worktreePath); removeErr != nil {
			return fmt.Errorf("failed to remove worktree: git error: %v; manual removal error: %v",
				string(output), removeErr)
		}
		fmt.Printf("Manually removed worktree directory\n")
	} else {
		fmt.Printf("Removed git worktree\n")
	}

	return nil
}
