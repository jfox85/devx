//go:build !windows

package target

import (
	"fmt"
	"os"
	"syscall"
)

func validateTrustedGatepostOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("could not inspect owner for trusted Gatepost path: %s", path)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("trusted Gatepost path owner uid %d does not match current uid %d: %s", stat.Uid, os.Getuid(), path)
	}
	return nil
}
