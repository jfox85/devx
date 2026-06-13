package target

import (
	"fmt"

	"github.com/jfox85/devx/session"
)

// SessionOperator owns tmux/session UX operations for a target runtime. It is
// deliberately separate from Target so future runtimes can keep lifecycle
// concerns independent from how DevX attaches terminals to sessions.
type SessionOperator interface {
	// IsRunning reports whether the runtime needed for session commands is available.
	IsRunning(meta session.TargetMeta) bool
	// EnsureTmuxSession ensures the session's tmux environment exists.
	EnsureTmuxSession(name string, sess *session.Session) error
	// AttachTmuxSession attaches the current terminal to the session's tmux environment.
	AttachTmuxSession(name string, sess *session.Session) error
	// KillTmuxServer stops any target-owned tmux server; no-op when not applicable.
	KillTmuxServer(meta session.TargetMeta) error
}

type hostSessionOperator struct{}
type dockerSessionOperator struct{}
type gatepostSessionOperator struct{}

// RuntimeName returns a human-readable runtime identifier for status messages.
func RuntimeName(meta session.TargetMeta) string {
	if meta.ContainerName != "" {
		return meta.ContainerName
	}
	return meta.Type
}

func ResolveSessionOperator(meta session.TargetMeta) (SessionOperator, error) {
	switch meta.Type {
	case "", "host":
		return hostSessionOperator{}, nil
	case "docker":
		return dockerSessionOperator{}, nil
	case "gatepost":
		return gatepostSessionOperator{}, nil
	default:
		return nil, fmt.Errorf("unknown target type %q (valid: host, docker, gatepost)", meta.Type)
	}
}

func (hostSessionOperator) IsRunning(_ session.TargetMeta) bool { return true }

func (hostSessionOperator) EnsureTmuxSession(name string, sess *session.Session) error {
	return session.EnsureTmuxSession(name, sess.Path)
}

func (op hostSessionOperator) AttachTmuxSession(name string, sess *session.Session) error {
	if err := op.EnsureTmuxSession(name, sess); err != nil {
		return err
	}
	return session.AttachTmuxSession(name)
}

func (hostSessionOperator) KillTmuxServer(_ session.TargetMeta) error { return nil }

func (dockerSessionOperator) IsRunning(meta session.TargetMeta) bool { return IsDockerRunning(meta) }

func (dockerSessionOperator) EnsureTmuxSession(name string, sess *session.Session) error {
	if sess.Target.ContainerName == "" {
		return fmt.Errorf("docker session %q has no runtime container", name)
	}
	return session.EnsureTmuxSessionInContainer(name, sess.Target.ContainerName, sess)
}

func (op dockerSessionOperator) AttachTmuxSession(name string, sess *session.Session) error {
	if err := op.EnsureTmuxSession(name, sess); err != nil {
		return err
	}
	return session.AttachTmuxSession(name)
}

func (dockerSessionOperator) KillTmuxServer(meta session.TargetMeta) error {
	if meta.ContainerName == "" {
		return nil
	}
	return ExecInSession(meta, []string{"tmux", "kill-server"}, false).Run()
}

func (gatepostSessionOperator) IsRunning(meta session.TargetMeta) bool { return IsDockerRunning(meta) }

func (gatepostSessionOperator) EnsureTmuxSession(name string, sess *session.Session) error {
	if sess.Target.ContainerName == "" {
		return fmt.Errorf("gatepost session %q has no runtime container", name)
	}
	return session.EnsureTmuxSessionInContainer(name, sess.Target.ContainerName, sess)
}

func (op gatepostSessionOperator) AttachTmuxSession(name string, sess *session.Session) error {
	if err := op.EnsureTmuxSession(name, sess); err != nil {
		return err
	}
	return session.AttachTmuxSession(name)
}

func (gatepostSessionOperator) KillTmuxServer(_ session.TargetMeta) error { return nil }

// IsRunning reports whether the target runtime needed for session commands is available.
func IsRunning(meta session.TargetMeta) bool {
	op, err := ResolveSessionOperator(meta)
	return err == nil && op.IsRunning(meta)
}

// EnsureTmuxSession ensures the session's tmux environment exists for its target.
func EnsureTmuxSession(name string, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	op, err := ResolveSessionOperator(sess.Target)
	if err != nil {
		return err
	}
	return op.EnsureTmuxSession(name, sess)
}

// AttachTmuxSession attaches the current terminal to the target's tmux session.
func AttachTmuxSession(name string, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	op, err := ResolveSessionOperator(sess.Target)
	if err != nil {
		return err
	}
	return op.AttachTmuxSession(name, sess)
}

// KillTmuxServer stops any target-owned tmux server; no-op when not applicable.
func KillTmuxServer(meta session.TargetMeta) error {
	op, err := ResolveSessionOperator(meta)
	if err != nil {
		return err
	}
	return op.KillTmuxServer(meta)
}
