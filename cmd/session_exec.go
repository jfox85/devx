package cmd

import (
	"fmt"
	"os"

	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
	"github.com/spf13/cobra"
)

var shellFlag bool

var sessionExecCmd = &cobra.Command{
	Use:   "exec <session> [-- <command...>]",
	Short: "Execute a command in a session's environment",
	Long: `Execute a command inside a session's execution environment.
For Docker sessions, this runs the command inside the container.
For host sessions, this runs the command in the worktree directory.

Use --shell to open an interactive shell instead of running a command.`,
	Args:               cobra.MinimumNArgs(1),
	DisableFlagParsing: false,
	RunE:               runSessionExec,
}

func init() {
	sessionCmd.AddCommand(sessionExecCmd)
	sessionExecCmd.Flags().BoolVar(&shellFlag, "shell", false, "Open an interactive shell")
}

func runSessionExec(cmd *cobra.Command, args []string) error {
	sessionName := args[0]

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	sess, exists := store.GetSession(sessionName)
	if !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	// Determine the command to run
	var execArgs []string
	if shellFlag {
		execArgs = []string{"/bin/bash"}
	} else if len(args) > 1 {
		execArgs = args[1:]
	} else {
		return fmt.Errorf("specify a command after -- or use --shell")
	}

	if sess.IsContainerized() {
		// Docker: check the container is running
		if !target.IsDockerRunning(sess.Target) {
			return fmt.Errorf("container for session '%s' is not running", sessionName)
		}

		execCmd := target.ExecInSession(sess.Target, execArgs, shellFlag)
		execCmd.Dir = sess.Path
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		return execCmd.Run()
	}

	// Host: run directly in the worktree
	hostCmd := target.ExecInSession(sess.Target, execArgs, false)
	hostCmd.Dir = sess.Path
	hostCmd.Stdin = os.Stdin
	hostCmd.Stdout = os.Stdout
	hostCmd.Stderr = os.Stderr
	return hostCmd.Run()
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
