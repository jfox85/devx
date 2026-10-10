//go:build !windows

package piagent

import "os"

// lockContended reports whether a failed lock mkdir at path means "someone
// else holds (or is just releasing) the lock", so the caller should wait and
// retry.
func lockContended(_ string, err error) bool { return os.IsExist(err) }
