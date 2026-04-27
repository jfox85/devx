package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/spf13/cobra"
)

var artifactListFlags struct {
	artifactType string
	tag          string
	agent        string
	search       string
	json         bool
}

var artifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts in a session",
	Args:  cobra.NoArgs,
	RunE:  runArtifactList,
}

func init() {
	artifactCmd.AddCommand(artifactListCmd)
	artifactListCmd.Flags().StringVar(&artifactSessionFlag, "session", "", "Session to list artifacts for (default: current session)")
	artifactListCmd.Flags().StringVar(&artifactListFlags.artifactType, "type", "", "Filter by artifact type")
	artifactListCmd.Flags().StringVar(&artifactListFlags.tag, "tag", "", "Filter by tag")
	artifactListCmd.Flags().StringVar(&artifactListFlags.agent, "agent", "", "Filter by agent")
	artifactListCmd.Flags().StringVar(&artifactListFlags.search, "search", "", "Search title, file, summary, tags, and ID")
	artifactListCmd.Flags().BoolVar(&artifactListFlags.json, "json", false, "Output artifacts as JSON")
}

func runArtifactList(cmd *cobra.Command, args []string) error {
	sess, err := resolveArtifactSession(artifactSessionFlag)
	if err != nil {
		return err
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		return err
	}
	items := artifactpkg.Filter(manifest.Artifacts, artifactpkg.FilterOptions{Type: artifactListFlags.artifactType, Tag: artifactListFlags.tag, Agent: artifactListFlags.agent, Search: artifactListFlags.search})
	computed := artifactpkg.WithComputedFields(sess.Name, items)
	if artifactListFlags.json {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(computed)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tTITLE\tCREATED\tRETENTION")
	for _, item := range computed {
		created := "-"
		if !item.Created.IsZero() {
			created = item.Created.Local().Format("2006-01-02 15:04")
		}
		retention := item.Retention
		if retention == "" {
			retention = artifactpkg.DefaultRetention
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Type, item.Title, created, retention)
	}
	return w.Flush()
}
