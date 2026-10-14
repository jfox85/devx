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
// then fails with ERROR_ACCESS_DENIED rather than ERROR_ALREADY_EXISTS, and
// the name may already look absent to a stat by the time we check. That is
// contention. A genuine permission problem (e.g. a read-only parent) also
// reports ACCESS_DENIED, and is told apart by persistence: see
// accessDeniedGrace in lock.go.
func lockContended(_ string, err error) bool {
	return os.IsExist(err) || isAccessDenied(err)
}

func isAccessDenied(err error) bool { return errors.Is(err, windows.ERROR_ACCESS_DENIED) }
