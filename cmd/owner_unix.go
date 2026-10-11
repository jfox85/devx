//go:build !windows

package cmd

import (
	"fmt"
	"os"
	"syscall"
)

func checkOwnedByMe(fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine owner")
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("not owned by the current user")
	}
	return nil
}
