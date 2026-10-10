//go:build windows

package piagent

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// ACCESS_DENIED is contention while it is brief (delete-pending lock dir),
// but an uninterrupted run longer than accessDeniedGrace is a real
// permission problem and must stop waiting.
func TestPersistentAccessDeniedIsNotWaitedOut(t *testing.T) {
	var d deniedTracker
	if d.persistent(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("first ACCESS_DENIED must be treated as contention")
	}
	d.since = time.Now().Add(-accessDeniedGrace - time.Millisecond)
	if !d.persistent(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("ACCESS_DENIED lasting past the grace must be reported")
	}
	if d.persistent(windows.ERROR_ALREADY_EXISTS) || !d.since.IsZero() {
		t.Fatal("any other result must reset the run")
	}
}
