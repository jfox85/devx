//go:build darwin || linux

package artifactbridge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// fileIdentity returns device, inode and ctime, so a replaced file never
// hits a cached hash even with an identical size and mtime.
func fileIdentity(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d:%v", st.Dev, st.Ino, ctimeOf(st))
	}
	return ""
}

// openUnderArtifacts opens <worktree>/.artifacts/<rel> for reading without
// following a symlink in ANY component (.artifacts itself, every directory,
// the leaf). Each component is opened relative to the already-open parent
// descriptor (openat + O_NOFOLLOW), so replacing a directory with a symlink
// between validation and open cannot redirect the read: there is no separate
// "check then open by path" step. The leaf must be a regular file with a
// single link (a hard link to a file outside the artifact tree is refused).
func openUnderArtifacts(worktree, rel string) (*os.File, os.FileInfo, error) {
	parts, err := splitRel(rel)
	if err != nil {
		return nil, nil, err
	}
	root, err := unix.Open(filepath.Clean(worktree), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, errUnavailable
	}
	dirfd := root
	closeDir := func() { _ = unix.Close(dirfd) }
	components := append([]string{artifactsDirName}, parts...)
	for i, p := range components {
		last := i == len(components)-1
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if last {
			flags |= unix.O_NONBLOCK // never block on a FIFO planted at the leaf
		} else {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(dirfd, p, flags, 0)
		closeDir()
		if err != nil {
			return nil, nil, errUnavailable
		}
		dirfd = fd
	}
	var st unix.Stat_t
	if err := unix.Fstat(dirfd, &st); err != nil {
		closeDir()
		return nil, nil, errUnavailable
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		closeDir()
		return nil, nil, errUnavailable
	}
	f := os.NewFile(uintptr(dirfd), strings.Join(components, "/"))
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, errUnavailable
	}
	return f, info, nil
}
