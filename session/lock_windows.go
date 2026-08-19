//go:build windows

package session

import "github.com/jfox85/devx/internal/filelock"

type lockFile struct {
	lock *filelock.Lock
}

func acquireSessionsLock(lockPath string) (*lockFile, error) {
	lock, err := filelock.Acquire(lockPath)
	if err != nil {
		return nil, err
	}
	return &lockFile{lock: lock}, nil
}

func (lock *lockFile) release() {
	if lock == nil {
		return
	}
	lock.lock.Release()
	lock.lock = nil
}
