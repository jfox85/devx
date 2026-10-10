//go:build windows

package session

import "golang.org/x/sys/windows"

// stillActive is the GetExitCodeProcess sentinel for a process that has not
// exited (STILL_ACTIVE / STATUS_PENDING).
const stillActive = 259

// processAlive reports whether pid refers to a running process. Windows has no
// signal 0 (os.Process.Signal only supports Kill there), so open the process
// with the least-privileged query right and check that it has not exited.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_ACCESS_DENIED means the PID exists but is protected (e.g. another
		// user or a higher integrity level), so it is running. Any other error
		// (typically ERROR_INVALID_PARAMETER) means there is no such process.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort handle release
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
