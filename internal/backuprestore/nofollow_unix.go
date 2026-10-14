//go:build !windows

package backuprestore

import (
	"os"
	"syscall"
)

// writeNewFileNoFollow creates path exclusively without following a symlink
// at the final component.
func writeNewFileNoFollow(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
