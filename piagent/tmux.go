package piagent

import (
	"fmt"
	"os/exec"
	"strings"
)

// Tmux runs tmux commands, optionally against an isolated server socket
// (tests use one so they never touch the user's sessions).
type Tmux struct {
	Socket string
}

func (t Tmux) args(a ...string) []string {
	if t.Socket != "" {
		return append([]string{"-L", t.Socket}, a...)
	}
	return a
}

func (t Tmux) Run(a ...string) (string, error) {
	out, err := exec.Command("tmux", t.args(a...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(a, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// PaneInfo describes a pane as tmux currently sees it.
type PaneInfo struct {
	Exists      bool
	SessionName string
	WindowID    string
	Dead        bool
	PID         string
}

// Pane looks up a pane by its stable %id.
func (t Tmux) Pane(paneID string) PaneInfo {
	if !strings.HasPrefix(paneID, "%") {
		return PaneInfo{}
	}
	out, err := t.Run("display-message", "-p", "-t", paneID, "#{pane_id}\t#{session_name}\t#{window_id}\t#{pane_dead}\t#{pane_pid}")
	if err != nil {
		return PaneInfo{}
	}
	f := strings.Split(out, "\t")
	if len(f) != 5 || f[0] != paneID {
		// tmux resolves unknown targets to the current pane in some
		// contexts; only an exact %id echo counts as a match.
		return PaneInfo{}
	}
	return PaneInfo{Exists: true, SessionName: f[1], WindowID: f[2], Dead: f[3] == "1", PID: f[4]}
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
