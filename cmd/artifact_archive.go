package cmd

import (
	"fmt"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/spf13/cobra"
)

var artifactArchiveCmd = &cobra.Command{
	Use:   "archive <id>",
	Short: "Mark an artifact for archive retention",
	Args:  cobra.ExactArgs(1),
	RunE:  runArtifactArchive,
}

func init() {
	artifactCmd.AddCommand(artifactArchiveCmd)
	artifactArchiveCmd.Flags().StringVar(&artifactSessionFlag, "session", "", "Session containing the artifact (default: current session)")
}

func runArtifactArchive(cmd *cobra.Command, args []string) error {
	sess, err := resolveArtifactSession(artifactSessionFlag)
	if err != nil {
		return err
	}
	updated, err := artifactpkg.SetRetention(sess, args[0], artifactpkg.ArchiveRetention)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Archived artifact %q\n", updated.ID)
	return nil
}
