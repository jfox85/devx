//go:build !windows

package session

import (
	"fmt"
	"os"
	"syscall"
)

// lockFile holds an advisory exclusive lock on the sessions metadata file.
type lockFile struct {
	f *os.File
}

// acquireSessionsLock opens (creating if needed) a sidecar lock file next to the
// sessions metadata and takes an exclusive advisory lock. The lock is released
// by calling release on the returned handle.
func acquireSessionsLock(lockPath string) (*lockFile, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open sessions lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to acquire sessions lock: %w", err)
	}
	return &lockFile{f: f}, nil
}

func (l *lockFile) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
