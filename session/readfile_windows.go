//go:build windows

package session

import (
	"os"

	"golang.org/x/sys/windows"
)

// openSessionsFileForRead opens the sessions metadata file for a lock-free
// read without blocking writers.
//
// LoadSessions is lock-free by design, and writers publish a new store by
// renaming a temp file over sessions.json. Go's os.Open on Windows does not
// pass FILE_SHARE_DELETE, so while any reader had the file open a concurrent
// writer's rename failed with "Access is denied" and its update was lost.
// Opening with FILE_SHARE_DELETE lets the rename replace the file under an
// open reader (the reader keeps the old contents), matching Unix semantics.
func openSessionsFileForRead(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}
