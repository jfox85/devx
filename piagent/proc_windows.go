//go:build windows

package piagent

import (
	"errors"
	"math"

	"golang.org/x/sys/windows"
)

// processAlive reports whether pid refers to a running process. Windows has
// no signal 0, so open the process with SYNCHRONIZE and do a zero-timeout
// wait: the process object is signaled once it exits. ACCESS_DENIED means the
// process exists but is protected (alive); any other open error means there
// is no such process. Used for stale-lock reclamation and bridge liveness.
// (Same probe as session.processAlive in the Windows portability PR #73.)
func processAlive(pid int) bool {
	if pid <= 0 || uint64(pid) > math.MaxUint32 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort handle release
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}
