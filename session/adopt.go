package session

import (
	"errors"
	"fmt"
)

// ErrAdoptNotAllowed is returned when a session cannot be adopted for an agent.
var ErrAdoptNotAllowed = errors.New("session cannot be adopted")

// AdoptManagedAgent records agentID as the managed agent of an existing,
// human-created session. It is idempotent for the same agent and refuses
// local-only sessions (they already belong to an MCP-created agent),
// sessions running in a container target, and sessions already adopted by a
// different agent. It changes no ports, routes or services.
func AdoptManagedAgent(name, agentID string) (*Session, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent id required")
	}
	var out *Session
	err := (&SessionStore{}).Mutate(func(fresh *SessionStore) error {
		s, ok := fresh.Sessions[name]
		switch {
		case !ok:
			return fmt.Errorf("session %q not found: %w", name, ErrAdoptNotAllowed)
		case s.IsLocalOnly():
			return fmt.Errorf("session %q is an MCP-created local-only session: %w", name, ErrAdoptNotAllowed)
		case s.IsContainerized():
			return fmt.Errorf("session %q runs in a %s container; only host sessions can be adopted: %w", name, s.TargetType(), ErrAdoptNotAllowed)
		case s.ManagedAgent != "" && s.ManagedAgent != agentID:
			return fmt.Errorf("session %q is already managed by agent %s: %w", name, s.ManagedAgent, ErrAdoptNotAllowed)
		}
		s.ManagedAgent = agentID
		cp := *s
		out = &cp
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseManagedAgent clears the adoption marker if it still names agentID.
func ReleaseManagedAgent(name, agentID string) error {
	return (&SessionStore{}).Mutate(func(fresh *SessionStore) error {
		if s, ok := fresh.Sessions[name]; ok && s.ManagedAgent == agentID {
			s.ManagedAgent = ""
		}
		return nil
	})
}
