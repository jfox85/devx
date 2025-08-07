package cmd

import (
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage multiple projects",
	Long:  `Manage multiple projects in devx, allowing you to work across different codebases with their own configurations.`,
}

func init() {
	rootCmd.AddCommand(projectCmd)
}
