//go:build !windows

package piagent

import "syscall"

func killPID(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
