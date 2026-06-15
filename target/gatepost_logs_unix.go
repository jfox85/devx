//go:build !windows

package target

import (
	"os/exec"
	"syscall"
)

func detachGatepostLogsProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
