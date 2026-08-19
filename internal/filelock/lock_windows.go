//go:build windows

package filelock

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type Lock struct {
	file *os.File
}

func Acquire(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	return &Lock{file: file}, nil
}

func (lock *Lock) Release() {
	if lock == nil || lock.file == nil {
		return
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &overlapped)
	_ = lock.file.Close()
	lock.file = nil
}
