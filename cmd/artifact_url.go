package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var artifactURLFlags struct {
	absolute bool
	local    bool
	embed    bool
}

var artifactURLCmd = &cobra.Command{
	Use:   "url <id>",
	Short: "Print the URL for an artifact",
	Args:  cobra.ExactArgs(1),
	RunE:  runArtifactURL,
}

func init() {
	artifactCmd.AddCommand(artifactURLCmd)
	artifactURLCmd.Flags().StringVar(&artifactSessionFlag, "session", "", "Session containing the artifact (default: current session)")
	artifactURLCmd.Flags().BoolVar(&artifactURLFlags.absolute, "absolute", false, "Print an absolute URL")
	artifactURLCmd.Flags().BoolVar(&artifactURLFlags.local, "local", false, "Print local .artifacts path")
	artifactURLCmd.Flags().BoolVar(&artifactURLFlags.embed, "embed", false, "Print same-session embed path (./file)")
}

func runArtifactURL(cmd *cobra.Command, args []string) error {
	sess, err := resolveArtifactSession(artifactSessionFlag)
	if err != nil {
		return err
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		return err
	}
	a, _ := artifactpkg.Find(manifest, args[0])
	if a == nil {
		return fmt.Errorf("artifact %q not found", args[0])
	}
	modeCount := 0
	if artifactURLFlags.local {
		modeCount++
	}
	if artifactURLFlags.embed {
		modeCount++
	}
	if artifactURLFlags.absolute {
		modeCount++
	}
	if modeCount > 1 {
		return fmt.Errorf("--local, --embed, and --absolute are mutually exclusive")
	}
	var out string
	switch {
	case artifactURLFlags.local:
		out = filepath.ToSlash(filepath.Join(artifactpkg.DirName, a.File))
	case artifactURLFlags.embed:
		out = "./" + filepath.ToSlash(a.File)
	case artifactURLFlags.absolute:
		out = absoluteArtifactURL(sess.Name, a.File)
	default:
		out = artifactpkg.WebPath(sess.Name, a.File)
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}

func absoluteArtifactURL(sessionName, file string) string {
	path := artifactpkg.WebPath(sessionName, file)
	external := strings.TrimSpace(viper.GetString("external_domain"))
	if external != "" {
		lower := strings.ToLower(external)
		if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
			return strings.TrimRight(external, "/") + path
		}
		return "https://" + strings.TrimRight(external, "/") + path
	}
	port := viper.GetInt("web_port")
	if port == 0 {
		port = 7777
	}
	return fmt.Sprintf("http://localhost:%d%s", port, path)
}
