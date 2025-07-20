package cmd

import (
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage development sessions",
	Long:  `Create and manage development sessions with Git worktrees.`,
}

var detachFlag bool

func init() {
	rootCmd.AddCommand(sessionCmd)
}