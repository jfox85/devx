//go:build windows

package cmd

import "os"

// checkOwnedByMe: file ownership is not checked on Windows (best-effort
// platform; the backup hash still binds the plan).
func checkOwnedByMe(os.FileInfo) error { return nil }

// openNoFollow: the caller already rejected a symlink with Lstat.
func openNoFollow(path string) (*os.File, error) { return os.Open(path) }
