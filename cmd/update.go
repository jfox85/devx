package cmd

import (
	"fmt"
	"os"

	"github.com/jfox85/devx/update"
	"github.com/spf13/cobra"
)

var (
	checkOnly   bool
	forceUpdate bool
)

// updateCmd represents the update command
var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update devx to the latest version",
	Long: `Update devx to the latest version from GitHub releases.

This command will:
- Check for the latest release on GitHub
- Download and replace the current binary if a newer version is available
- Preserve the installation method when possible
- Show progress during download

Note: If devx was installed via Homebrew, you'll be directed to use 'brew upgrade' instead.

Examples:
  devx update              # Update to latest version
  devx update --check      # Only check for updates
  devx update --force      # Force update even if same version`,
	Run: func(cmd *cobra.Command, args []string) {
		if checkOnly {
			checkForUpdates()
			return
		}

		performUpdate()
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)

	updateCmd.Flags().BoolVar(&checkOnly, "check", false, "Only check for updates without downloading")
	updateCmd.Flags().BoolVar(&forceUpdate, "force", false, "Force update even if current version is latest")
}

// checkForUpdates checks if a newer version is available
func checkForUpdates() {
	fmt.Println("Checking for updates...")

	info, err := update.CheckForUpdates()
	if err != nil {
		fmt.Printf("Error checking for updates: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Current version: %s\n", info.CurrentVersion)
	fmt.Printf("Latest version:  %s\n", info.LatestVersion)

	if !info.Available {
		fmt.Println("✅ You are running the latest version!")
		return
	}

	fmt.Printf("🆙 A newer version is available: %s → %s\n", info.CurrentVersion, info.LatestVersion)
	fmt.Printf("Release URL: %s\n", info.ReleaseURL)
	fmt.Println("\nRun 'devx update' to upgrade.")
}

// performUpdate downloads and installs the latest version
func performUpdate() {
	// Check if we can self-update
	if !update.CanSelfUpdate() {
		fmt.Println(update.GetUpdateInstructions())
		return
	}

	fmt.Println("Checking for updates...")

	// Check for updates first
	info, err := update.CheckForUpdates()
	if err != nil {
		fmt.Printf("Error checking for updates: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Current version: %s\n", info.CurrentVersion)
	fmt.Printf("Latest version:  %s\n", info.LatestVersion)

	// Check if update is needed
	if !info.Available && !forceUpdate {
		fmt.Println("✅ You are already running the latest version!")
		return
	}

	if forceUpdate && !info.Available {
		fmt.Println("Forcing update due to --force flag...")
	} else {
		fmt.Printf("🔄 Updating from %s to %s...\n", info.CurrentVersion, info.LatestVersion)
	}

	fmt.Println("📥 Downloading update...")

	// Perform the update
	if err := update.PerformUpdate(forceUpdate); err != nil {
		fmt.Printf("❌ Update failed: %v\n", err)
		os.Exit(1)
	}
}
