//go:build darwin || linux

package artifactbridge

import "golang.org/x/sys/unix"

func mkfifo(p string) error { return unix.Mkfifo(p, 0o644) }
