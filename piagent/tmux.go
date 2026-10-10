package piagent

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Tmux runs tmux commands. Production uses the zero value (the user's tmux
// server, addressed only by exact =session / %pane targets; DevX never kills
// a tmux server). Tests set Exec to a tmuxfixture guard, which pins every
// command to a fixture-owned socket and rejects anything else before it
// runs.
type Tmux struct {
	// Exec, when set, receives the argv (without "tmux") instead of running
	// tmux directly.
	Exec func(args ...string) (string, error)
}

func (t Tmux) Run(a ...string) (string, error) {
	if t.Exec != nil {
		return t.Exec(a...)
	}
	out, err := exec.Command("tmux", a...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(a, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// PaneInfo describes a pane as tmux currently sees it.
type PaneInfo struct {
	Exists bool
	// QueryFailed means tmux could not be asked (server busy or restarting,
	// timeout, tmux missing). It is NOT evidence that the pane is gone.
	QueryFailed bool
	SessionName string
	WindowID    string
	Dead        bool
	PID         string
	// LinkedSessions lists every session the pane's window is linked into.
	// Grouped sessions (e.g. DevX's "<name>-web" viewer) share windows, and
	// tmux reports whichever of them was used most recently as SessionName.
	LinkedSessions []string
	// Command is the pane's current foreground command (pane_current_command).
	Command string
}

// InSession reports whether the pane's window belongs to session, either as
// the session tmux reports or through a linked/grouped session.
func (p PaneInfo) InSession(session string) bool {
	if !p.Exists || session == "" {
		return false
	}
	if p.SessionName == session {
		return true
	}
	for _, s := range p.LinkedSessions {
		if s == session {
			return true
		}
	}
	return false
}

// Pane looks up a pane by its stable %id.
func (t Tmux) Pane(paneID string) PaneInfo {
	if !strings.HasPrefix(paneID, "%") {
		return PaneInfo{}
	}
	out, err := t.Run("display-message", "-p", "-t", paneID, "#{pane_id}\t#{session_name}\t#{window_id}\t#{pane_dead}\t#{pane_pid}\t#{window_linked_sessions_list}\t#{pane_current_command}")
	if err != nil {
		if paneAbsent(err) {
			return PaneInfo{}
		}
		return PaneInfo{QueryFailed: true}
	}
	f := strings.Split(out, "\t")
	if len(f) < 5 || f[0] != paneID {
		// tmux resolves unknown targets to the current pane in some
		// contexts; only an exact %id echo counts as a match.
		return PaneInfo{}
	}
	p := PaneInfo{Exists: true, SessionName: f[1], WindowID: f[2], Dead: f[3] == "1", PID: f[4]}
	if len(f) > 5 && f[5] != "" {
		p.LinkedSessions = strings.Split(f[5], ",")
	}
	if len(f) > 6 {
		p.Command = f[6]
	}
	return p
}

// ErrTmuxUnavailable means tmux could not be queried, so pane state is
// unknown. Callers fail closed and change nothing.
var ErrTmuxUnavailable = errors.New("tmux could not be queried")

// paneAbsent reports whether a display-message error is tmux confirming the
// target does not exist (as opposed to tmux being unreachable). No server on
// the socket (connection refused, or a missing socket) also counts: panes cannot outlive their server, and treating it
// as a query failure would block Start's resume (which restarts the server)
// forever.
func paneAbsent(err error) bool {
	s := err.Error()
	for _, m := range []string{"can't find pane", "can't find window", "can't find session", "no server running"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	// "error connecting to" covers every connect errno; only a missing
	// socket (ENOENT) means no server. EACCES, ENAMETOOLONG etc. say nothing
	// about the pane and stay query failures.
	return strings.Contains(s, "error connecting to") && strings.Contains(s, "No such file or directory")
}

// HasSession reports whether an exact tmux session name exists.
func (t Tmux) HasSession(name string) bool {
	_, err := t.Run("has-session", "-t", "="+name)
	return err == nil
}

// NewWindow creates a detached window running command and returns its ids.
func (t Tmux) NewWindow(session, name, dir, command string) (windowID, paneID string, err error) {
	out, err := t.Run("new-window", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-t", "="+session+":", "-n", name, "-c", dir, command)
	if err != nil {
		return "", "", err
	}
	ids := strings.Fields(out)
	if len(ids) != 2 || !strings.HasPrefix(ids[0], "@") || !strings.HasPrefix(ids[1], "%") {
		return "", "", fmt.Errorf("tmux returned invalid window identifiers %q", out)
	}
	return ids[0], ids[1], nil
}

// RespawnPane restarts command in an existing pane, killing what runs there.
func (t Tmux) RespawnPane(paneID, dir, command string) error {
	_, err := t.Run("respawn-pane", "-k", "-t", paneID, "-c", dir, command)
	return err
}

// Capture returns the visible text of a pane plus some scrollback.
func (t Tmux) Capture(paneID string, lines int) (string, error) {
	return t.Run("capture-pane", "-p", "-J", "-t", paneID, "-S", fmt.Sprintf("-%d", lines))
}
