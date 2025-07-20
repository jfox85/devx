package cmd

import (
	"github.com/jfox85/devx/deps"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check system dependencies",
	Long:  `Check if all required and optional dependencies are installed and available.`,
	Run:   runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)
}

func runCheck(cmd *cobra.Command, args []string) {
	results := deps.CheckAllDependencies()
	editorResult := deps.CheckConfiguredEditor()
	deps.PrintResults(results, editorResult)
}