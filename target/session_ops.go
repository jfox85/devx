package target

import (
	"fmt"
	"os/exec"

	"github.com/jfox85/devx/session"
)

// RuntimeName returns a human-readable runtime identifier for status messages.
func RuntimeName(meta session.TargetMeta) string {
	if meta.ContainerName != "" {
		return meta.ContainerName
	}
	return meta.Type
}

// IsRunning reports whether the target runtime needed for session commands is available.
func IsRunning(meta session.TargetMeta) bool {
	switch meta.Type {
	case "", "host":
		return true
	case "docker", "gatepost":
		return IsDockerRunning(meta)
	default:
		return false
	}
}

// EnsureTmuxSession ensures the session's tmux environment exists for its target.
func EnsureTmuxSession(name string, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	meta := sess.Target
	switch meta.Type {
	case "", "host":
		return session.EnsureTmuxSession(name, sess.Path)
	case "docker", "gatepost":
		if meta.ContainerName == "" {
			return fmt.Errorf("%s session %q has no runtime container", meta.Type, name)
		}
		return session.EnsureTmuxSessionInContainer(name, meta.ContainerName, sess)
	default:
		return fmt.Errorf("unsupported target type %q", meta.Type)
	}
}

// AttachTmuxSession attaches the current terminal to the target's tmux session.
func AttachTmuxSession(name string, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	meta := sess.Target
	switch meta.Type {
	case "", "host", "docker", "gatepost":
		if err := EnsureTmuxSession(name, sess); err != nil {
			return err
		}
		return session.AttachTmuxSession(name)
	default:
		return fmt.Errorf("unsupported target type %q", meta.Type)
	}
}

// KillTmuxServerCommand returns a command that stops the target tmux server, if applicable.
func KillTmuxServerCommand(meta session.TargetMeta) *exec.Cmd {
	if meta.Type == "docker" {
		return ExecInSession(meta, []string{"tmux", "kill-server"}, false)
	}
	return nil
}
