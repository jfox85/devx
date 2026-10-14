//go:build darwin || linux

package artifactbridge

import (
	"fmt"
	"os"
	"syscall"
)

func openPolicy(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}

// policyFileTrusted refuses a config that another local user could have
// written: it must be owned by this process's user and not group- or
// world-writable. Such a file would let that user widen the bridge scope.
func policyFileTrusted(fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("config ownership cannot be verified")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("config is not owned by the current user")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("config is writable by group or others")
	}
	return nil
}
