package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var sessionClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove all development sessions",
	Long: `Remove all development sessions, their worktrees, and associated Caddy routes.

This command will:
- Stop all tmux sessions
- Remove all git worktrees
- Delete all Caddy routes
- Clear the sessions registry

WARNING: This cannot be undone!`,
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")

		// Load existing sessions
		registry, err := session.LoadRegistry()
		if err != nil {
			fmt.Printf("Error loading sessions: %v\n", err)
			os.Exit(1)
		}

		if len(registry.Sessions) == 0 {
			fmt.Println("No sessions to clear.")
			return
		}

		// Show what will be removed
		fmt.Printf("This will remove %d sessions:\n", len(registry.Sessions))
		for name := range registry.Sessions {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Println()

		// Confirm unless --force is used
		if !force {
			fmt.Print("Are you sure? (y/N): ")
			reader := bufio.NewReader(os.Stdin)
			response, err := reader.ReadString('\n')
			if err != nil {
				fmt.Printf("Error reading input: %v\n", err)
				os.Exit(1)
			}

			response = strings.TrimSpace(strings.ToLower(response))
			if response != "y" && response != "yes" {
				fmt.Println("Aborted")
				return
			}
		}

		// Remove all sessions
		var errors []string
		for name, sess := range registry.Sessions {
			fmt.Printf("Removing session '%s'...\n", name)

			if err := session.RemoveSession(name, sess); err != nil {
				errors = append(errors, fmt.Sprintf("Failed to remove session '%s': %v", name, err))
				continue
			}
		}

		// Clear the registry
		if err := session.ClearRegistry(); err != nil {
			errors = append(errors, fmt.Sprintf("Failed to clear registry: %v", err))
		}

		// Report results
		if len(errors) > 0 {
			fmt.Printf("\nCompleted with %d errors:\n", len(errors))
			for _, err := range errors {
				fmt.Printf("  - %s\n", err)
			}
			os.Exit(1)
		} else {
			fmt.Printf("\nSuccessfully removed all %d sessions.\n", len(registry.Sessions))
		}
	},
}

func init() {
	sessionCmd.AddCommand(sessionClearCmd)
	sessionClearCmd.Flags().Bool("force", false, "Skip confirmation prompt")
}
