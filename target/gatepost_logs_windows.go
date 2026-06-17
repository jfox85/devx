//go:build windows

package target

import (
	"os"
	"os/exec"
)

func detachGatepostLogsProcess(_ *exec.Cmd) {}

func stopGatepostLogsProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
