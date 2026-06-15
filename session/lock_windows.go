//go:build windows

package session

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile holds an exclusive lock on the sessions metadata file via the
// Windows LockFileEx API, mirroring the flock-based lock used on Unix so that
// concurrent devx processes cannot clobber each other's sessions.json updates.
//
// Note: Windows is not a supported devx runtime (devx targets macOS/Linux hosts
// with Docker Desktop). This implementation exists so the package cross-compiles
// and provides best-effort mutual exclusion; the atomic-rename publish step in
// writeStoreAtomic is also only guaranteed atomic on Unix.
type lockFile struct {
	f *os.File
}

// acquireSessionsLock opens (creating if needed) a sidecar lock file and takes
// an exclusive, blocking byte-range lock on it. The lock is released by calling
// release on the returned handle.
func acquireSessionsLock(lockPath string) (*lockFile, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to open sessions lock file: %w", err)
	}
	// Lock a single byte range; LOCKFILE_EXCLUSIVE_LOCK with no
	// LOCKFILE_FAIL_IMMEDIATELY flag blocks until the lock is available.
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1, 0,
		&overlapped,
	); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to acquire sessions lock: %w", err)
	}
	return &lockFile{f: f}, nil
}

func (l *lockFile) release() {
	if l == nil || l.f == nil {
		return
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &overlapped)
	_ = l.f.Close()
	l.f = nil
}
