package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var artifactAddFlags struct {
	artifactType string
	title        string
	summary      string
	agent        string
	retention    string
	tags         string
	focus        bool
	id           string
	file         string
}

var artifactAddCmd = &cobra.Command{
	Use:   "add [flags] <file|->",
	Short: "Add a file to the current session's artifacts",
	Args:  cobra.ExactArgs(1),
	RunE:  runArtifactAdd,
}

func init() {
	artifactCmd.AddCommand(artifactAddCmd)
	artifactAddCmd.Flags().StringVar(&artifactSessionFlag, "session", "", "Session to add the artifact to (default: current session)")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.artifactType, "type", "", "Artifact type (plan|report|screenshot|recording|log|diff|document|other)")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.title, "title", "", "Human-readable artifact title (required)")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.summary, "summary", "", "One-line artifact summary")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.agent, "agent", "", "Agent identifier (default: $DEVX_AGENT or unknown)")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.retention, "retention", "", "Artifact retention: session or archive (default: $DEVX_ARTIFACT_RETENTION or session)")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.tags, "tags", "", "Comma-separated tags")
	artifactAddCmd.Flags().BoolVar(&artifactAddFlags.focus, "focus", false, "Flag the session for attention and auto-open this artifact")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.id, "id", "", "Custom artifact ID")
	artifactAddCmd.Flags().StringVar(&artifactAddFlags.file, "file", "", "Destination path under .artifacts/ (required when reading from stdin)")
}

func runArtifactAdd(cmd *cobra.Command, args []string) error {
	if artifactAddFlags.title == "" {
		return fmt.Errorf("--title is required")
	}
	sess, err := resolveArtifactSession(artifactSessionFlag)
	if err != nil {
		return err
	}
	agent := artifactAddFlags.agent
	if agent == "" {
		agent = os.Getenv("DEVX_AGENT")
	}
	retention := artifactAddFlags.retention
	if retention == "" {
		retention = os.Getenv("DEVX_ARTIFACT_RETENTION")
	}
	source := args[0]
	opts := artifactpkg.AddOptions{
		Source:      source,
		Destination: artifactAddFlags.file,
		ID:          artifactAddFlags.id,
		Type:        artifactAddFlags.artifactType,
		Title:       artifactAddFlags.title,
		Summary:     artifactAddFlags.summary,
		Agent:       agent,
		Retention:   retention,
		Tags:        artifactpkg.ParseTags(artifactAddFlags.tags),
		Focus:       artifactAddFlags.focus,
	}
	if source == "-" {
		if artifactAddFlags.file == "" {
			return fmt.Errorf("--file is required when reading artifact content from stdin")
		}
		opts.Reader = cmd.InOrStdin()
		if opts.Reader == nil {
			opts.Reader = os.Stdin
		}
	}
	added, err := artifactpkg.Add(sess, opts)
	if err != nil {
		return err
	}
	if artifactAddFlags.focus {
		if err := session.SetAttentionFlagWithSource(sess.Name, "New artifact: "+artifactAddFlags.title, "artifact"); err != nil {
			return fmt.Errorf("artifact added, but failed to set attention flag: %w", err)
		}
		notifyWebServer(sess.Name, true, "New artifact: "+artifactAddFlags.title)
		notifyArtifactWebServer(sess.Name, added.ID)
	}
	fmt.Fprintln(cmd.OutOrStdout(), artifactpkg.WebPath(sess.Name, added.File))
	return nil
}

func notifyArtifactWebServer(sessionName, artifactID string) {
	token := os.Getenv("DEVX_WEB_SECRET_TOKEN")
	if token == "" {
		// viper is initialized for commands, but keep env fallback-friendly.
		token = viper.GetString("web_secret_token")
	}
	port := viper.GetInt("web_port")
	if token == "" || port == 0 {
		return
	}
	q := url.Values{}
	q.Set("session", sessionName)
	q.Set("id", artifactID)
	addr := fmt.Sprintf("http://localhost:%d/api/artifacts/notify?%s", port, q.Encode())
	req, err := http.NewRequest(http.MethodPost, addr, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
