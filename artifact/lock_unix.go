//go:build !windows

package artifact

import (
	"os"
	"syscall"
)

func lockManifestFile(lockFile *os.File) error {
	return syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX)
}

func unlockManifestFile(lockFile *os.File) error {
	return syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
}
