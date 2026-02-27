package web

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

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
		// Production mode: launch ttyd with --base-path so all asset URLs are absolute,
		// allowing devx web to proxy the full /terminal/{session}/* path space.
		args := []string{
			"-p", fmt.Sprintf("%d", port),
			"-W",
			"--base-path", "/terminal/" + sessionName,
			"tmux", "attach", "-t", sessionName,
		}
		cmd = exec.Command("ttyd", args...)
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
	}

	return port, nil
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
func (m *ttydManager) portForSession(name string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.sessions[name]; ok {
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

// stopSession kills the ttyd process for a session.
func (m *ttydManager) stopSession(sessionName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.sessions[sessionName]
	if !ok {
		return
	}
	if inst.cmd != nil && inst.cmd.Process != nil {
		inst.cmd.Process.Kill() //nolint:errcheck
	}
	delete(m.sessions, sessionName)
}
