//go:build windows

package artifact

import "os"

func lockManifestFile(lockFile *os.File) error {
	return nil
}

func unlockManifestFile(lockFile *os.File) error {
	return nil
}
