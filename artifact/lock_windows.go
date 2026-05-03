//go:build windows

package artifact

import (
	"os"

	"golang.org/x/sys/windows"
)

const wholeFileLockSize = ^uint32(0)

func lockManifestFile(lockFile *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.LockFileEx(
		windows.Handle(lockFile.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		wholeFileLockSize,
		wholeFileLockSize,
		overlapped,
	)
}

func unlockManifestFile(lockFile *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.UnlockFileEx(
		windows.Handle(lockFile.Fd()),
		0,
		wholeFileLockSize,
		wholeFileLockSize,
		overlapped,
	)
}
