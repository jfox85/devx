//go:build windows

package piagent

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockContended reports whether a failed lock mkdir means "someone else holds
// (or is just releasing) the lock", so the caller should wait and retry.
//
// On Windows, a directory that its holder has just removed stays
// delete-pending until every open handle to it closes; mkdir on that name
// then fails with ERROR_ACCESS_DENIED rather than ERROR_ALREADY_EXISTS. That
// is the same contention, not a permission problem, so it must be retried
// within the timeout instead of failing the caller.
func lockContended(err error) bool {
	return os.IsExist(err) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
