//go:build !windows

package cloudflare

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// DefaultPIDPath returns the default path for the cloudflared PID file.
func DefaultPIDPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "devx", "cloudflared.pid")
}

// StartDaemon launches cloudflared as a background process using the given
// config file and tunnel ID. Returns the PID of the new process.
// If cloudflared is already running (valid PID file), returns an error.
func StartDaemon(cfgPath, tunnelID, pidPath string) (int, error) {
	// Check if already running
	if pid, err := ReadPID(pidPath); err == nil {
		if IsRunning(pid) {
			return 0, fmt.Errorf("cloudflared is already running (pid %d)", pid)
		}
		// Stale PID file — clean it up
		_ = os.Remove(pidPath)
	}

	binary, err := exec.LookPath("cloudflared")
	if err != nil {
		return 0, fmt.Errorf("cloudflared not found in PATH: %w", err)
	}

	// Expand ~ in cfgPath
	if strings.HasPrefix(cfgPath, "~/") {
		home, _ := os.UserHomeDir()
		cfgPath = filepath.Join(home, cfgPath[2:])
	}

	// Log file next to PID file
	logPath := strings.TrimSuffix(pidPath, ".pid") + ".log"
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to open log file: %w", err)
	}

	cmd := exec.Command(binary, "tunnel", "--config", cfgPath, "run", tunnelID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("failed to start cloudflared: %w", err)
	}
	logFile.Close()

	pid := cmd.Process.Pid
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("failed to write PID file: %w", err)
	}

	return pid, nil
}

// StopDaemon sends SIGTERM to the cloudflared process identified by pidPath.
func StopDaemon(pidPath string) (int, error) {
	pid, err := ReadPID(pidPath)
	if err != nil {
		return 0, fmt.Errorf("cloudflared is not running")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return 0, fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(pidPath)
		return 0, fmt.Errorf("failed to stop process %d: %w", pid, err)
	}

	_ = os.Remove(pidPath)
	return pid, nil
}

// ReadPID reads the PID from the given file. Returns an error if the file
// doesn't exist or is malformed.
func ReadPID(pidPath string) (int, error) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("malformed PID file: %w", err)
	}
	return pid, nil
}

// IsRunning returns true if a process with the given PID exists and is alive.
func IsRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks process existence without sending a real signal
	return proc.Signal(syscall.Signal(0)) == nil
}

// ReloadDaemon restarts the cloudflared daemon so it picks up an updated
// config file. If the daemon is not running, this is a no-op (returns nil).
func ReloadDaemon(cfgPath, tunnelID, pidPath string) error {
	pid, err := ReadPID(pidPath)
	if err != nil || !IsRunning(pid) {
		// Not running — nothing to reload
		return nil
	}

	if _, err := StopDaemon(pidPath); err != nil {
		return fmt.Errorf("failed to stop cloudflared for reload: %w", err)
	}

	if _, err := StartDaemon(cfgPath, tunnelID, pidPath); err != nil {
		return fmt.Errorf("failed to restart cloudflared: %w", err)
	}

	return nil
}
