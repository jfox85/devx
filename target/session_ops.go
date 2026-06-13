package target

import (
	"fmt"

	"github.com/jfox85/devx/session"
)

// RuntimeName returns a human-readable runtime identifier for status messages.
func RuntimeName(meta session.TargetMeta) string {
	if meta.ContainerName != "" {
		return meta.ContainerName
	}
	return meta.Type
}

func targetForMeta(meta session.TargetMeta) (Target, error) {
	return Resolve(meta.Type)
}

// IsRunning reports whether the target runtime needed for session commands is available.
func IsRunning(meta session.TargetMeta) bool {
	tgt, err := targetForMeta(meta)
	return err == nil && tgt.IsRunning(meta)
}

// EnsureTmuxSession ensures the session's tmux environment exists for its target.
func EnsureTmuxSession(name string, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	tgt, err := targetForMeta(sess.Target)
	if err != nil {
		return err
	}
	return tgt.EnsureTmuxSession(name, sess)
}

// AttachTmuxSession attaches the current terminal to the target's tmux session.
func AttachTmuxSession(name string, sess *session.Session) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	tgt, err := targetForMeta(sess.Target)
	if err != nil {
		return err
	}
	return tgt.AttachTmuxSession(name, sess)
}

// KillTmuxServer stops any target-owned tmux server; no-op when not applicable.
func KillTmuxServer(meta session.TargetMeta) error {
	tgt, err := targetForMeta(meta)
	if err != nil {
		return err
	}
	return tgt.KillTmuxServer(meta)
}
