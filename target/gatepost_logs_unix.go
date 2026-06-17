//go:build !windows

package target

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func detachGatepostLogsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopGatepostLogsProcessGroup(pid int) error {
	// gatepost-logs is started in a detached process group; kill the group so
	// wrappers like `go run` do not leave the actual log server behind.
	groupErr := syscall.Kill(-pid, syscall.SIGKILL)
	if groupErr == nil || errors.Is(groupErr, os.ErrProcessDone) || errors.Is(groupErr, syscall.ESRCH) {
		return nil
	}
	p, findErr := os.FindProcess(pid)
	if findErr != nil {
		return fmt.Errorf("kill gatepost-logs process group %d: %w; find pid %d: %w", pid, groupErr, pid, findErr)
	}
	if killErr := p.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return fmt.Errorf("kill gatepost-logs process group %d: %w; fallback kill pid %d: %w", pid, groupErr, pid, killErr)
	}
	return nil
}
