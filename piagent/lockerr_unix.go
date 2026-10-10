//go:build !windows

package piagent

import "os"

// lockContended reports whether a failed lock mkdir means "someone else holds
// (or is just releasing) the lock", so the caller should wait and retry.
func lockContended(err error) bool { return os.IsExist(err) }
