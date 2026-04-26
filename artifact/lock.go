package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/jfox85/devx/session"
)

func withManifestLock(sess *session.Session, fn func() error) error {
	dir := DirForSession(sess)
	if err := EnsureArtifactDir(dir); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}
	lockPath := filepath.Join(dir, ".manifest.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open artifact manifest lock: %w", err)
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to lock artifact manifest: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return fn()
}
