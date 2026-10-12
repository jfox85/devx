//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"syscall"
)

// openRunFileNoFollow opens path read-only without following a final symlink.
func openRunFileNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// checkRunPerms refuses a file or directory that is writable by group or
// others or not owned by the current user.
func checkRunPerms(fi os.FileInfo) error {
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("writable by group or others")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine owner")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("not owned by the current user")
	}
	return nil
}
