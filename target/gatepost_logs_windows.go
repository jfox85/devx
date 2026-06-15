//go:build windows

package target

import "os/exec"

func detachGatepostLogsProcess(_ *exec.Cmd) {}
