//go:build !windows

package session

import "os"

// replaceFile atomically publishes src at dst. On Unix rename(2) already
// replaces dst even while readers hold it open.
func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
