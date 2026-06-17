//go:build !windows

package target

import (
	"os"
	"os/exec"
	"syscall"
)

func detachGatepostLogsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopGatepostLogsProcessGroup(pid int) {
	// gatepost-logs is started in a detached process group; kill the group so
	// wrappers like `go run` do not leave the actual log server behind.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
