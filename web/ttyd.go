package web

import (
	"fmt"
	"os/exec"
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
// "ttyd -p <port> -W tmux attach -t <session>" (used for testing).
func (m *ttydManager) startForSession(sessionName string, cmdAndArgs ...string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if inst, ok := m.sessions[sessionName]; ok {
		if inst.timer != nil {
			inst.timer.Stop()
			inst.timer = nil
		}
		return inst.port, nil
	}

	portResult, err := getport.GetPort(getport.TCP, "")
	if err != nil {
		return 0, fmt.Errorf("failed to allocate port: %w", err)
	}
	port := portResult.Port

	var cmd *exec.Cmd
	if len(cmdAndArgs) > 0 {
		// Testing mode: use provided command directly (first arg is binary)
		cmd = exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)
	} else {
		// Production mode: launch ttyd
		args := []string{"-p", fmt.Sprintf("%d", port), "-W", "tmux", "attach", "-t", sessionName}
		cmd = exec.Command("ttyd", args...)
	}

	if err := cmd.Start(); err != nil {
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

	return port, nil
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
