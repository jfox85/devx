//go:build !windows

package session

import "os"

// openSessionsFileForRead opens the sessions metadata file for a lock-free
// read. On Unix an open reader never blocks a writer's atomic rename.
func openSessionsFileForRead(path string) (*os.File, error) {
	return os.Open(path)
}
