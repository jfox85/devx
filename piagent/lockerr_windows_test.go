//go:build windows

package piagent

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// ACCESS_DENIED for a lock path that does not exist is a real permission
// problem, not a delete-pending lock: it must not be retried until timeout.
func TestAccessDeniedOnAbsentPathIsNotContention(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such.lock")
	if lockContended(missing, windows.ERROR_ACCESS_DENIED) {
		t.Fatal("ACCESS_DENIED with the lock path absent must not count as contention")
	}
	present := t.TempDir()
	if !lockContended(present, windows.ERROR_ACCESS_DENIED) {
		t.Fatal("ACCESS_DENIED with the lock path present (delete-pending) must count as contention")
	}
}
