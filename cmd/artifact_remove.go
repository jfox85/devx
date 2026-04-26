package cmd

import (
	"fmt"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/spf13/cobra"
)

var artifactRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove an artifact by ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runArtifactRemove,
}

func init() {
	artifactCmd.AddCommand(artifactRemoveCmd)
	artifactRemoveCmd.Flags().StringVar(&artifactSessionFlag, "session", "", "Session containing the artifact (default: current session)")
}

func runArtifactRemove(cmd *cobra.Command, args []string) error {
	sess, err := resolveArtifactSession(artifactSessionFlag)
	if err != nil {
		return err
	}
	removed, err := artifactpkg.Remove(sess, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed artifact %q (%s)\n", removed.ID, removed.File)
	return nil
}
