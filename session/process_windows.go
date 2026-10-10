//go:build windows

package session

import (
	"errors"
	"math"

	"golang.org/x/sys/windows"
)

// processAlive reports whether pid refers to a running process. Windows has no
// signal 0 (os.Process.Signal only supports Kill there), so open the process
// with SYNCHRONIZE and do a zero-timeout wait: a process object is signaled
// once it exits. This avoids GetExitCodeProcess's STILL_ACTIVE (259)
// ambiguity, where a process that exited with code 259 looks alive.
func processAlive(pid int) bool {
	if uint64(pid) > math.MaxUint32 {
		// Corrupt metadata; never let it wrap onto a real PID.
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// ERROR_ACCESS_DENIED means the PID exists but is protected (e.g. another
		// user or a higher integrity level), so it is running. Any other error
		// (typically ERROR_INVALID_PARAMETER) means there is no such process.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort handle release
	event, err := windows.WaitForSingleObject(h, 0)
	return err == nil && event == uint32(windows.WAIT_TIMEOUT)
}
