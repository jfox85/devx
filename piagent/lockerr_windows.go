//go:build windows

package piagent

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockContended reports whether a failed lock mkdir at path means "someone
// else holds (or is just releasing) the lock", so the caller should wait and
// retry.
//
// On Windows, a lock directory its holder has just removed stays
// delete-pending until every open handle to it closes; mkdir on that name
// then fails with ERROR_ACCESS_DENIED rather than ERROR_ALREADY_EXISTS. That
// is contention. A genuine permission problem (e.g. a read-only parent) also
// reports ACCESS_DENIED, but then the lock path itself is absent, so it is
// returned at once instead of being retried until the timeout.
func lockContended(path string, err error) bool {
	if os.IsExist(err) {
		return true
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return false
	}
	_, statErr := os.Lstat(path)
	return !os.IsNotExist(statErr) // present or delete-pending (stat also denied)
}
