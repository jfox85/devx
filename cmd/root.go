/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/deps"
	"github.com/jfox85/devx/tui"
	"github.com/jfox85/devx/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "devx",
	Short: "A macOS development environment manager",
	Long: `devx is a CLI tool for managing local development environments on macOS.
It integrates with Caddy, tmuxp, and other tools to streamline project setup and management.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/devx/config.yaml)")

	// Add version flag to root command
	rootCmd.Flags().BoolP("version", "v", false, "Show version information")

	// Override the default behavior to handle --version/-v
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if versionFlag, _ := cmd.Flags().GetBool("version"); versionFlag {
			fmt.Println(version.Get().String())
			return nil
		}

		// Original TUI behavior
		if len(args) == 0 {
			checkDependenciesQuiet()
			return runTUI()
		}

		return nil
	}
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Check for project-level config first, then fall back to global config
		projectConfigDir := config.FindProjectConfigDir()
		if projectConfigDir != "" {
			viper.AddConfigPath(projectConfigDir)
		}

		// Also add global config path as fallback
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)
		globalConfigPath := home + "/.config/devx"
		viper.AddConfigPath(globalConfigPath)

		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
	}

	viper.SetEnvPrefix("DEVX")
	viper.AutomaticEnv() // read in environment variables that match

	// Set default values
	viper.SetDefault("basedomain", "localhost")
	viper.SetDefault("caddy_api", "http://localhost:2019")
	viper.SetDefault("tmuxp_template", config.GetTmuxTemplatePath())
	viper.SetDefault("ports", []string{"ui", "api"})
	viper.SetDefault("editor", "")
	viper.SetDefault("bootstrap_files", []string{})
	viper.SetDefault("cleanup_command", "")

	// Read in config file if found
	viper.ReadInConfig()
}

// runTUI launches the terminal user interface
func runTUI() error {
	return tui.Run()
}

// checkDependenciesQuiet performs a quick check and shows warnings for missing dependencies
func checkDependenciesQuiet() {
	results := deps.CheckAllDependencies()

	var missingRequired []string
	var missingOptional []string

	for _, result := range results {
		if !result.Available {
			if result.Dependency.Required {
				missingRequired = append(missingRequired, result.Dependency.Name)
			} else {
				missingOptional = append(missingOptional, result.Dependency.Name)
			}
		}
	}

	// Check configured editor
	if editorResult := deps.CheckConfiguredEditor(); editorResult != nil && !editorResult.Available {
		missingOptional = append(missingOptional, "Editor")
	}

	// Show warnings if needed
	if len(missingRequired) > 0 {
		fmt.Printf("⚠️  Warning: Missing required dependencies: %s\n", strings.Join(missingRequired, ", "))
		fmt.Printf("   Run 'devx check' for installation instructions.\n\n")
	}

	if len(missingOptional) > 0 {
		fmt.Printf("ℹ️  Note: Missing optional dependencies: %s\n", strings.Join(missingOptional, ", "))
		fmt.Printf("   Run 'devx check' for more details.\n\n")
	}
}
