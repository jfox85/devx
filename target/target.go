// Package target defines the execution environment abstraction for DevX sessions.
// A Target controls where a session's processes run: on the host, in a Docker
// container, or (future) in a VM or remote server.
package target

import (
	"context"
	"fmt"

	"github.com/jfox85/devx/session"
)

// Target is the interface that isolation backends implement.
type Target interface {
	// Type returns the target identifier ("host", "docker", etc.)
	Type() string
	// Start creates and starts the execution environment for a session.
	Start(ctx context.Context, opts StartOpts) (*StartResult, error)
	// Stop tears down the execution environment. Must be idempotent.
	Stop(ctx context.Context, meta session.TargetMeta) error
	// IsRunning reports whether the runtime needed for session commands is available.
	IsRunning(meta session.TargetMeta) bool
	// EnsureTmuxSession ensures the session's tmux environment exists for this target.
	EnsureTmuxSession(name string, sess *session.Session) error
	// AttachTmuxSession attaches the current terminal to this target's tmux session.
	AttachTmuxSession(name string, sess *session.Session) error
	// KillTmuxServer stops any target-owned tmux server; no-op when not applicable.
	KillTmuxServer(meta session.TargetMeta) error
}

// StartOpts contains everything a target needs to create a session environment.
type StartOpts struct {
	SessionName    string
	WorktreePath   string
	HostPorts      map[string]int // service name -> host port to publish
	Image          string
	Env            map[string]string
	Labels         map[string]string
	Security       SecurityOpts
	GatepostConfig GatepostRuntimeConfig
}

// GatepostRuntimeConfig is the trusted host-side contract DevX passes to the
// Gatepost target. Executable command paths should come from explicit env/CLI
// or user-global config, never from project repo config.
type GatepostRuntimeConfig struct {
	Root                     string
	LogsCommand              string
	ProviderBootstrapCommand string
	AuthHome                 string
	RequiredProviders        string
}

// StartResult is returned by Target.Start with metadata to persist.
type StartResult struct {
	Meta session.TargetMeta
}

// Resolve returns the Target implementation for the given type string.
func Resolve(targetType string) (Target, error) {
	switch targetType {
	case "", "host":
		return &HostTarget{}, nil
	case "docker":
		return &DockerTarget{}, nil
	case "gatepost":
		return &GatepostTarget{}, nil
	default:
		return nil, fmt.Errorf("unknown target type %q (valid: host, docker, gatepost)", targetType)
	}
}
