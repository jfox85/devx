//go:build windows

package target

import "os"

func validateTrustedGatepostOwner(_ string, _ os.FileInfo) error {
	// Windows does not expose syscall.Stat_t UIDs, so there is no owner check.
	// The mode check in validateTrustedGatepostPath still runs and fails closed
	// on Windows (directories report 0777), so trust is never granted there.
	return nil
}
