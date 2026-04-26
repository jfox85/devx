package web

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jfox85/devx/session"
	getport "github.com/jsumners/go-getport"
)

type ttydInstance struct {
	port  int
	cmd   *exec.Cmd
	conns int
	timer *time.Timer
}

type ttydManager struct {
	mu       sync.Mutex
	sessions map[string]*ttydInstance
}

func newTtydManager() *ttydManager {
	return &ttydManager{sessions: make(map[string]*ttydInstance)}
}

const ttydIdleTimeout = 30 * time.Second
const ttydScrollbackLines = 5000

func ttydArgs(sessionName string, port int) []string {
	webSession := sessionName + "-web"
	return []string{
		"-p", fmt.Sprintf("%d", port),
		"-i", "127.0.0.1", // bind to loopback only — not reachable from the network
		"-W",
		"--base-path", "/terminal/" + sessionName,
		"-t", fmt.Sprintf("scrollback=%d", ttydScrollbackLines),
		"-t", "fontFamily=HackNerdFontMono, monospace",
		"tmux", "new-session", "-A", "-s", webSession, "-t", "=" + sessionName,
	}
}

// startForSession returns the local port of the ttyd instance for the session,
// starting one if not already running. cmdAndArgs overrides the default
// "ttyd -p <port> -W --base-path ... tmux attach -t <session>" (used for testing).
func (m *ttydManager) startForSession(sessionName string, cmdAndArgs ...string) (int, error) {
	m.mu.Lock()

	if inst, ok := m.sessions[sessionName]; ok {
		if inst.timer != nil {
			inst.timer.Stop()
			inst.timer = nil
		}
		port := inst.port
		m.mu.Unlock()
		return port, nil
	}

	portResult, err := getport.GetPort(getport.TCP, "")
	if err != nil {
		m.mu.Unlock()
		return 0, fmt.Errorf("failed to allocate port: %w", err)
	}
	port := portResult.Port

	var cmd *exec.Cmd
	if len(cmdAndArgs) > 0 {
		// Testing mode: use provided command directly (first arg is binary)
		cmd = exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)
	} else {
		// Verify the base tmux session exists before launching ttyd. Without this
		// check, "tmux new-session -A -s <name>-web -t <name>" silently creates a
		// standalone session (bare shell) when the target doesn't exist.
		if exec.Command("tmux", "has-session", "-t", "="+sessionName).Run() != nil {
			m.mu.Unlock()
			return 0, fmt.Errorf("tmux session %q does not exist", sessionName)
		}

		// Production mode: launch ttyd with --base-path so all asset URLs are absolute,
		// allowing devx web to proxy the full /terminal/{session}/* path space.
		//
		// Use "tmux new-session -A -s <name>-web -t <name>" instead of plain
		// "tmux attach" so the web client gets its own grouped session with
		// independent sizing. This prevents the browser window dimensions from
		// constraining the terminal session used by the real terminal client.
		cmd = exec.Command("ttyd", ttydArgs(sessionName, port)...)
	}

	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return 0, fmt.Errorf("failed to start ttyd: %w", err)
	}

	m.sessions[sessionName] = &ttydInstance{port: port, cmd: cmd}

	// Clean up map entry when process exits. Pass cmd as parameter to avoid
	// deleting a newly-started replacement instance if the session is restarted
	// before this goroutine runs.
	go func(trackedCmd *exec.Cmd) {
		trackedCmd.Wait() //nolint:errcheck
		m.mu.Lock()
		if inst, ok := m.sessions[sessionName]; ok && inst.cmd == trackedCmd {
			delete(m.sessions, sessionName)
		}
		m.mu.Unlock()
	}(cmd)

	// Release the lock before waiting — ttyd needs time to bind its port and
	// we must not hold the mutex during a blocking operation.
	m.mu.Unlock()

	// Only wait for the port in production mode. In testing mode (cmdAndArgs provided)
	// the stub command (e.g. "sleep") never binds to a port, so we skip the check.
	if len(cmdAndArgs) == 0 {
		if err := waitForPort(port, 5*time.Second); err != nil {
			// ttyd failed to start; clean up
			m.mu.Lock()
			if inst, ok := m.sessions[sessionName]; ok && inst.cmd == cmd {
				delete(m.sessions, sessionName)
			}
			m.mu.Unlock()
			_ = cmd.Process.Kill()
			return 0, fmt.Errorf("ttyd did not start on port %d: %w", port, err)
		}
		applyMobileTmuxOptions(sessionName)
	}

	return port, nil
}

func applyMobileTmuxOptions(sessionName string) {
	baseTarget := "=" + sessionName
	_ = exec.Command("tmux", "set-option", "-t", baseTarget, "mouse", session.DefaultTmuxMouse).Run()
	_ = exec.Command("tmux", "set-option", "-t", baseTarget, "history-limit", fmt.Sprintf("%d", session.DefaultTmuxHistoryLimit)).Run()

	webTarget := "=" + sessionName + "-web"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if exec.Command("tmux", "has-session", "-t", webTarget).Run() == nil {
			mouseErr := exec.Command("tmux", "set-option", "-t", webTarget, "mouse", session.DefaultTmuxMouse).Run()
			historyErr := exec.Command("tmux", "set-option", "-t", webTarget, "history-limit", fmt.Sprintf("%d", session.DefaultTmuxHistoryLimit)).Run()
			if mouseErr != nil || historyErr != nil {
				logWebError("applyMobileTmuxOptions(%q): web target update incomplete: mouse=%v history=%v", sessionName, mouseErr, historyErr)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	logWebError("applyMobileTmuxOptions(%q): web target %q not available before timeout; history-limit is not retroactive for existing windows", sessionName, webTarget)
}

// waitForPort polls addr until it accepts a TCP connection or the timeout elapses.
func waitForPort(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("localhost:%d", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}

// clientConnected increments the connection count and cancels the idle timer.
func (m *ttydManager) clientConnected(sessionName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.sessions[sessionName]; ok {
		inst.conns++
		if inst.timer != nil {
			inst.timer.Stop()
			inst.timer = nil
		}
	}
}

// clientDisconnected decrements the connection count and starts the idle timer.
func (m *ttydManager) clientDisconnected(sessionName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.sessions[sessionName]
	if !ok {
		return
	}
	if inst.conns > 0 {
		inst.conns--
	}
	if inst.conns == 0 {
		inst.timer = time.AfterFunc(ttydIdleTimeout, func() {
			m.stopSession(sessionName)
		})
	}
}

// portForSession returns the port of a running ttyd instance, if one exists.
// It also cancels any pending idle timer so the instance is not killed while
// a new HTTP or WebSocket connection is being established.
func (m *ttydManager) portForSession(name string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.sessions[name]; ok {
		if inst.timer != nil {
			inst.timer.Stop()
			inst.timer = nil
		}
		return inst.port, true
	}
	return 0, false
}

// findSessionByPathPrefix finds the running session whose name is the longest prefix
// of path. Used to route asset requests from ttyd's HTML where slashes are unencoded.
func (m *ttydManager) findSessionByPathPrefix(path string) (name string, port int, found bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for n, inst := range m.sessions {
		if (strings.HasPrefix(path, n+"/") || path == n) && len(n) > len(name) {
			name = n
			port = inst.port
			found = true
		}
	}
	return
}

// stopSession kills the ttyd process for a session and cleans up the grouped
// tmux session that was created for the web client.
func (m *ttydManager) stopSession(sessionName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.sessions[sessionName]
	if !ok {
		return
	}
	// A client may have reconnected between when the idle timer was started and
	// when this callback fired. Re-check under the lock so we don't kill an
	// instance that is actively in use.
	if inst.conns > 0 {
		return
	}
	if inst.cmd != nil && inst.cmd.Process != nil {
		inst.cmd.Process.Kill() //nolint:errcheck
	}
	delete(m.sessions, sessionName)
	// Kill the grouped tmux session that was created for the web client so it
	// doesn't linger after the web view is closed.
	go exec.Command("tmux", "kill-session", "-t", "="+sessionName+"-web").Run() //nolint:errcheck
}
