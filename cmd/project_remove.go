package cmd

import (
	"fmt"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var forceRemove bool

var projectRemoveCmd = &cobra.Command{
	Use:     "remove <alias>",
	Short:   "Remove a project from devx",
	Aliases: []string{"rm"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		alias := args[0]

		// Load project registry
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return fmt.Errorf("failed to load project registry: %w", err)
		}

		// Check if project exists
		project, err := registry.GetProject(alias)
		if err != nil {
			return err
		}

		// Check for existing sessions unless force flag is used
		if !forceRemove {
			store, err := session.LoadSessions()
			if err != nil {
				return fmt.Errorf("failed to load sessions: %w", err)
			}

			// Count sessions for this project
			var sessionCount int
			for _, sess := range store.Sessions {
				if sess.ProjectAlias == alias {
					sessionCount++
				}
			}

			if sessionCount > 0 {
				return fmt.Errorf("project '%s' has %d active session(s). Use --force to remove anyway", alias, sessionCount)
			}
		}

		// Remove project
		if err := registry.RemoveProject(alias); err != nil {
			return fmt.Errorf("failed to remove project: %w", err)
		}

		fmt.Printf("Removed project '%s' (%s)\n", alias, project.Name)

		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectRemoveCmd)

	projectRemoveCmd.Flags().BoolVarP(&forceRemove, "force", "f", false, "Force removal even if sessions exist")
}
