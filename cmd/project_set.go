package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jfox85/devx/config"
	"github.com/spf13/cobra"
)

var projectSetCmd = &cobra.Command{
	Use:   "set <alias> <key> <value>",
	Short: "Set a project configuration value",
	Long: `Set a configuration value for a specific project.

Available keys:
  name             - Display name of the project
  description      - Description of the project
  default-branch   - Default branch for new sessions (e.g., main, master)
  auto-pull        - Automatically pull from origin when creating sessions (true/false)

Examples:
  devx project set myapp auto-pull true
  devx project set backend default-branch develop
  devx project set frontend description "React frontend application"`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		alias := args[0]
		key := args[1]
		value := args[2]

		// Load project registry
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return fmt.Errorf("failed to load project registry: %w", err)
		}

		// Get the project
		project, err := registry.GetProject(alias)
		if err != nil {
			return err
		}

		// Update the specified field
		switch key {
		case "name":
			project.Name = value
			fmt.Printf("Set project '%s' name to: %s\n", alias, value)

		case "description", "desc":
			project.Description = value
			fmt.Printf("Set project '%s' description to: %s\n", alias, value)

		case "default-branch":
			project.DefaultBranch = value
			fmt.Printf("Set project '%s' default branch to: %s\n", alias, value)

		case "auto-pull":
			// Parse boolean value
			boolValue, err := strconv.ParseBool(strings.ToLower(value))
			if err != nil {
				return fmt.Errorf("auto-pull must be true or false")
			}
			project.AutoPullOnCreate = boolValue
			fmt.Printf("Set project '%s' auto-pull to: %v\n", alias, boolValue)

		default:
			return fmt.Errorf("unknown configuration key: %s\n\nAvailable keys: name, description, default-branch, auto-pull", key)
		}

		// Save the updated registry
		if err := registry.Save(); err != nil {
			return fmt.Errorf("failed to save project registry: %w", err)
		}

		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectSetCmd)
}
