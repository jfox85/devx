package cmd

import (
	"fmt"
	"os"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var artifactSessionFlag string

var artifactCmd = &cobra.Command{
	Use:   "artifact",
	Short: "Manage session artifacts",
	Long:  `Manage rich output files attached to devx sessions, such as plans, reports, screenshots, recordings, logs, and reference documents.`,
}

func init() {
	rootCmd.AddCommand(artifactCmd)
}

func resolveArtifactSession(name string) (*session.Session, error) {
	store, err := session.LoadSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to load sessions: %w", err)
	}
	if name == "" {
		name = os.Getenv("SESSION_NAME")
	}
	if name == "" {
		name = session.GetCurrentSessionName()
	}
	if name == "" {
		return nil, fmt.Errorf("could not determine current devx session; run from a session worktree or pass --session")
	}
	sess, ok := store.GetSession(name)
	if !ok {
		return nil, fmt.Errorf("session %q not found", name)
	}
	// Allow overriding the session path (e.g. /workspace inside a container
	// where the host worktree path doesn't exist).
	if override := os.Getenv("DEVX_SESSION_PATH"); override != "" {
		sess.Path = override
	} else if _, err := os.Stat(sess.Path); os.IsNotExist(err) {
		// Auto-detect container context: if sess.Path doesn't exist but
		// /workspace does, we're likely inside a Gatepost container.
		if info, wsErr := os.Stat("/workspace"); wsErr == nil && info.IsDir() {
			sess.Path = "/workspace"
		}
	}
	return sess, nil
}
