//go:build windows

package piagent

// Managed Pi agents require tmux and are not supported on Windows. Treat the
// process as alive so status never misreports a running task as unknown.
func processAlive(pid int) bool { return pid > 0 }
