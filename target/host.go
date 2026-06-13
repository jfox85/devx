package target

import (
	"context"

	"github.com/jfox85/devx/session"
)

// HostTarget is the default target — sessions run directly on the host.
// Start and Stop are no-ops since the host environment is always available.
type HostTarget struct{}

func (h *HostTarget) Type() string { return "host" }

func (h *HostTarget) Start(_ context.Context, _ StartOpts) (*StartResult, error) {
	return &StartResult{Meta: session.TargetMeta{Type: "host"}}, nil
}

func (h *HostTarget) Stop(_ context.Context, _ session.TargetMeta) error {
	return nil
}

func (h *HostTarget) IsRunning(_ session.TargetMeta) bool { return true }

func (h *HostTarget) EnsureTmuxSession(name string, sess *session.Session) error {
	return session.EnsureTmuxSession(name, sess.Path)
}

func (h *HostTarget) AttachTmuxSession(name string, sess *session.Session) error {
	if err := h.EnsureTmuxSession(name, sess); err != nil {
		return err
	}
	return session.AttachTmuxSession(name)
}

func (h *HostTarget) KillTmuxServer(_ session.TargetMeta) error { return nil }
