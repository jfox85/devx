package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var projectListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all registered projects",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		// Load project registry
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return fmt.Errorf("failed to load project registry: %w", err)
		}

		if len(registry.Projects) == 0 {
			fmt.Println("No projects registered.")
			fmt.Println("\nAdd a project with: devx project add <path> --alias <alias>")
			return nil
		}

		// Load sessions to count per project
		store, err := session.LoadSessions()
		if err != nil {
			return fmt.Errorf("failed to load sessions: %w", err)
		}

		// Count sessions per project
		sessionCounts := make(map[string]int)
		for _, sess := range store.Sessions {
			if sess.ProjectAlias != "" {
				sessionCounts[sess.ProjectAlias]++
			}
		}

		// Create tabwriter for aligned output
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "ALIAS\tNAME\tPATH\tSESSIONS\tDESCRIPTION\n")
		fmt.Fprintf(w, "-----\t----\t----\t--------\t-----------\n")

		for alias, project := range registry.Projects {
			count := sessionCounts[alias]
			desc := project.Description
			if desc == "" {
				desc = "-"
			}

			// Check if path still exists
			pathStatus := project.Path
			if _, err := os.Stat(project.Path); err != nil {
				pathStatus = fmt.Sprintf("%s (missing)", project.Path)
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
				alias,
				project.Name,
				pathStatus,
				count,
				desc,
			)
		}

		w.Flush()

		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectListCmd)
}
