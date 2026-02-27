package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// ensureWebDaemonRunning starts the devx web daemon if it is not already running.
// It checks the PID file first to avoid double-starting.
func ensureWebDaemonRunning() error {
	pidPath := webDaemonPIDPath()

	// Check if already running
	if data, err := os.ReadFile(pidPath); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				// Signal(0) checks if the process exists without sending a signal
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					return nil // already running
				}
			}
		}
	}

	// Start the daemon
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}
	cmd := exec.Command(self, "web", "--daemon")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start web daemon: %w", err)
	}
	return nil
}

func webDaemonPIDPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "devx", "web.pid")
}
