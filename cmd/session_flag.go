package cmd

import (
	"fmt"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	clearFlag     bool
	forceFlagFlag bool
)

var sessionFlagCmd = &cobra.Command{
	Use:   "flag <name> [reason]",
	Short: "Flag a session for attention",
	Long: `Flag a session to indicate it needs attention. This will show a visual indicator 
in the TUI and can be used by external tools (like Claude Code) to signal when work is complete.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runSessionFlag,
}

func init() {
	sessionCmd.AddCommand(sessionFlagCmd)
	sessionFlagCmd.Flags().BoolVar(&clearFlag, "clear", false, "Clear the attention flag instead of setting it")
	sessionFlagCmd.Flags().BoolVar(&forceFlagFlag, "force", false, "Force flagging even if it's the current session")
}

func runSessionFlag(cmd *cobra.Command, args []string) error {
	sessionName := args[0]

	if clearFlag {
		// Clear the flag
		if err := session.ClearAttentionFlag(sessionName); err != nil {
			return fmt.Errorf("failed to clear attention flag: %w", err)
		}
		fmt.Printf("Cleared attention flag for session '%s'\n", sessionName)
		return nil
	}

	// Set the flag
	reason := "manual"
	if len(args) > 1 {
		reason = args[1]
	}

	// Check if this is the current session (unless forced)
	if !forceFlagFlag {
		currentSession := session.GetCurrentSessionName()
		if currentSession == sessionName {
			fmt.Printf("Not flagging session '%s' because it's currently active (use --force to override)\n", sessionName)
			return nil
		}
	}

	if err := session.SetAttentionFlag(sessionName, reason); err != nil {
		return fmt.Errorf("failed to set attention flag: %w", err)
	}

	fmt.Printf("Flagged session '%s' for attention (reason: %s)\n", sessionName, reason)
	return nil
}
