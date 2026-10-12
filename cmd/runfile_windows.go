//go:build windows

package cmd

import "os"

// openRunFileNoFollow: the caller already rejected a symlink with Lstat.
func openRunFileNoFollow(path string) (*os.File, error) { return os.Open(path) }

// checkRunPerms: POSIX owner and mode bits are not meaningful on Windows
// (Go reports writable files as 0666), so they are not checked there
// (best-effort platform; the location, directory name, plan hash and
// journal membership checks still apply).
func checkRunPerms(os.FileInfo) error { return nil }
