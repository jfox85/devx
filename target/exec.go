package target

import (
	"os/exec"

	"github.com/jfox85/devx/session"
)

// ExecInSession builds an exec.Cmd that runs a command in the session's
// execution environment. For host sessions it runs the command directly.
// For Docker sessions it wraps with docker exec.
//
// The caller is responsible for setting Stdin/Stdout/Stderr and running
// the command.
func ExecInSession(meta session.TargetMeta, cmd []string, interactive bool) *exec.Cmd {
	if meta.Type == "" || meta.Type == "host" {
		return exec.Command(cmd[0], cmd[1:]...)
	}
	// Docker: prefix with docker exec
	args := []string{"exec"}
	if interactive {
		args = append(args, "-it")
	}
	args = append(args, meta.ContainerName)
	args = append(args, cmd...)
	return exec.Command("docker", args...)
}
