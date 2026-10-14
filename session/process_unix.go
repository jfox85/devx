//go:build !windows

package session

import (
	"os"
	"syscall"
)

// processAlive reports whether pid exists by sending signal 0, which performs
// the existence/permission check without delivering a signal.
func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
