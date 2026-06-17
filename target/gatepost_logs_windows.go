//go:build windows

package target

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

func detachGatepostLogsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func stopGatepostLogsProcessGroup(pid int) error {
	// taskkill /T terminates the wrapper plus child process tree, covering
	// trusted-root launches that use `go run ./cmd/gatepost-logs`.
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err == nil {
		return nil
	}
	p, findErr := os.FindProcess(pid)
	if findErr != nil {
		return fmt.Errorf("find gatepost-logs pid %d: %w", pid, findErr)
	}
	if killErr := p.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return fmt.Errorf("kill gatepost-logs pid %d: %w", pid, killErr)
	}
	return nil
}
