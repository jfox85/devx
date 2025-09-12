package cmd

import (
	"fmt"
	"os"

	"github.com/jfox85/devx/claude"
	"github.com/spf13/cobra"
)

var claudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Manage Claude Code integration",
	Long:  `Commands for managing Claude Code integration, including hook installation and configuration.`,
}

var claudeForceFlag bool
var noBackupFlag bool
var dryRunFlag bool
var quietFlag bool

var initHooksCmd = &cobra.Command{
	Use:   "init-hooks",
	Short: "Initialize Claude hooks for session notifications",
	Long: `Install Claude hooks that automatically update session flags when Claude stops
or is waiting for input. This enables seamless integration with devx session management.

The hooks will:
- Set session flag to "Claude Done" when Claude stops
- Set session flag to "Claude is waiting for your input" during notifications

By default, this command will create a backup of existing settings before making changes.`,
	RunE: runInitHooks,
}

func init() {
	rootCmd.AddCommand(claudeCmd)
	claudeCmd.AddCommand(initHooksCmd)

	// Flags for init-hooks command
	initHooksCmd.Flags().BoolVarP(&claudeForceFlag, "force", "f", false, "overwrite existing hooks")
	initHooksCmd.Flags().BoolVar(&noBackupFlag, "no-backup", false, "don't create backup of existing settings")
	initHooksCmd.Flags().BoolVar(&dryRunFlag, "dry-run", false, "show what would be done without making changes")
	initHooksCmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress output (useful for TUI integration)")
}

func runInitHooks(cmd *cobra.Command, args []string) error {
	// Get current working directory as project path
	projectPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Handle dry-run mode
	if dryRunFlag {
		preview, err := claude.PreviewChanges(projectPath)
		if err != nil {
			return fmt.Errorf("failed to generate preview: %w", err)
		}

		if !quietFlag {
			fmt.Print("Dry run - changes that would be made:\n\n")
			fmt.Print(preview)
		}
		return nil
	}

	// Check current status
	hooksInstalled, err := claude.CheckHooksStatus(projectPath)
	if err != nil {
		return fmt.Errorf("failed to check hooks status: %w", err)
	}

	if hooksInstalled && !claudeForceFlag {
		if !quietFlag {
			fmt.Println("Claude hooks are already installed and configured correctly.")
			fmt.Println("Use --force to reinstall them anyway.")
		}
		return nil
	}

	// Install hooks
	createBackup := !noBackupFlag
	result, err := claude.InstallHooks(projectPath, claudeForceFlag, createBackup)
	if err != nil {
		return fmt.Errorf("failed to install hooks: %w", err)
	}

	// Output results
	if !quietFlag {
		if result.AlreadyExists {
			fmt.Println(result.Message)
		} else {
			fmt.Println(result.Message)

			if result.BackupCreated {
				fmt.Printf("Backup created: %s\n", result.BackupPath)
			}

			if result.Created {
				fmt.Println("\nCreated .claude/settings.local.json with hooks configuration.")
			} else if result.Updated {
				fmt.Println("\nUpdated .claude/settings.local.json with hooks configuration.")
			}

			fmt.Println("\nThe following hooks have been configured:")
			fmt.Println("• Stop: Sets session flag to 'Claude Done'")
			fmt.Println("• Notification: Sets session flag to 'Claude is waiting for your input'")
			fmt.Println("\nYour Claude Code sessions in this project will now automatically")
			fmt.Println("update the session flags when interacting with devx.")
		}
	}

	return nil
}
