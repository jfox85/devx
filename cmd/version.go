package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/jfox85/devx/update"
	"github.com/jfox85/devx/version"
	"github.com/spf13/cobra"
)

var (
	versionOutput string
	detailedFlag  bool
	checkUpdates  bool
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Display version information for devx including build details.`,
	Run:   runVersion,
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().StringVarP(&versionOutput, "output", "o", "", "Output format: json")
	versionCmd.Flags().BoolVar(&detailedFlag, "detailed", false, "Show detailed version information")
	versionCmd.Flags().BoolVar(&checkUpdates, "check-updates", false, "Check for available updates")
}

func runVersion(cmd *cobra.Command, args []string) {
	info := version.Get()

	switch versionOutput {
	case "json":
		output, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			fmt.Printf("Error formatting JSON: %v\n", err)
			return
		}
		fmt.Println(string(output))
	default:
		if detailedFlag {
			fmt.Println(info.Detailed())
		} else {
			fmt.Println(info.String())
		}
	}

	// Check for updates if requested
	if checkUpdates {
		fmt.Println() // Add blank line
		checkForVersionUpdates()
	}
}

// checkForVersionUpdates checks if a newer version is available
func checkForVersionUpdates() {
	fmt.Println("Checking for updates...")

	updateInfo, err := update.CheckForUpdates()
	if err != nil {
		fmt.Printf("Error checking for updates: %v\n", err)
		return
	}

	fmt.Printf("Current version: %s\n", updateInfo.CurrentVersion)
	fmt.Printf("Latest version:  %s\n", updateInfo.LatestVersion)

	if !updateInfo.Available {
		fmt.Println("✅ You are running the latest version!")
		return
	}

	fmt.Printf("🆙 A newer version is available: %s → %s\n", updateInfo.CurrentVersion, updateInfo.LatestVersion)
	fmt.Printf("Release URL: %s\n", updateInfo.ReleaseURL)
	fmt.Println("\nRun 'devx update' to upgrade.")

	// Mark as notified so we don't spam on every command
	if err := update.MarkUpdateNotified(updateInfo.LatestVersion); err != nil {
		// Silently ignore - notification state is not critical
		_ = err
	}
}
