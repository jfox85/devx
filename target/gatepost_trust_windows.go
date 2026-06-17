//go:build windows

package target

import "os"

func validateTrustedGatepostOwner(_ string, _ os.FileInfo) error {
	// Windows CI/builds do not expose syscall.Stat_t UIDs. Keep the mode and
	// symlink/path-scope checks portable there.
	return nil
}
