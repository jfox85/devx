//go:build windows

package backuprestore

import "os"

// writeNewFileNoFollow creates path exclusively. O_EXCL refuses an existing
// file or link at the final component.
func writeNewFileNoFollow(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
