//go:build darwin || linux

package artifactbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// platformSupported: the bridge relies on openat/O_NOFOLLOW/linkat. Other
// platforms disable both capabilities (fail closed).
const platformSupported = true

// fileIdentity returns device, inode and ctime. ctime cannot be set from
// user space and changes on every content or metadata write, so together
// with size and mtime it witnesses that a file's bytes are unchanged.
func fileIdentity(fi os.FileInfo) string {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d:%d", st.Dev, st.Ino, ctimeOf(st))
	}
	return ""
}

// openDirChain opens <worktree>/<parts...> as a directory descriptor without
// following a symlink in any component below the worktree. Each component is
// opened relative to its already-open parent (openat + O_NOFOLLOW), so
// swapping a directory for a symlink between checks and use cannot redirect
// the operation. With create, missing directories are made with mkdirat.
// The worktree path itself comes from DevX's own session metadata (it must
// equal the agent record), never from the caller.
func openDirChain(worktree string, parts []string, create bool) (int, error) {
	fd, err := unix.Open(filepath.Clean(worktree), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, errUnavailable
	}
	for _, p := range parts {
		next, err := unix.Openat(fd, p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if merr := unix.Mkdirat(fd, p, 0o755); merr != nil && !errors.Is(merr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, errUnavailable
			}
			next, err = unix.Openat(fd, p, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		_ = unix.Close(fd)
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return -1, errNotExist
			}
			return -1, errUnavailable
		}
		fd = next
	}
	return fd, nil
}

// openUnderArtifacts opens <worktree>/.artifacts/<rel> for reading without
// following a symlink in ANY component (.artifacts, every directory, the
// leaf). The leaf must be a regular file with a single link: a FIFO or device
// is refused without blocking (O_NONBLOCK), and a hard link to a file outside
// the artifact tree is refused.
func openUnderArtifacts(worktree, rel string) (*os.File, os.FileInfo, error) {
	parts, err := splitRel(rel)
	if err != nil {
		return nil, nil, err
	}
	dirfd, err := openDirChain(worktree, append([]string{artifactsDirName}, parts[:len(parts)-1]...), false)
	if err != nil {
		return nil, nil, err
	}
	defer unix.Close(dirfd)
	return openLeaf(dirfd, parts[len(parts)-1], strings.Join(parts, "/"))
}

func openLeaf(dirfd int, name, label string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, nil, errNotExist
		}
		return nil, nil, errUnavailable
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		_ = unix.Close(fd)
		return nil, nil, errUnavailable
	}
	f := os.NewFile(uintptr(fd), label)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, errUnavailable
	}
	return f, info, nil
}

// attachmentsDir opens (creating if needed) .artifacts/attachments with the
// same no-follow guarantees.
func attachmentsDir(worktree string) (int, error) {
	return openDirChain(worktree, []string{artifactsDirName, AttachmentsFolder}, true)
}

func closeDir(fd int) { _ = unix.Close(fd) }

// writeTemp writes data to a fresh file named name in dirfd (O_EXCL|
// O_NOFOLLOW). A pre-existing entry of that name can only be ours from an
// interrupted attempt; it is unlinked first, which removes a directory entry
// and never follows a link.
func writeTemp(dirfd int, name string, data []byte) error {
	if err := unix.Unlinkat(dirfd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return errUnavailable
	}
	fd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o644)
	if err != nil {
		return errUnavailable
	}
	f := os.NewFile(uintptr(fd), name)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return errUnavailable
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return errUnavailable
	}
	if err := f.Close(); err != nil {
		return errUnavailable
	}
	return nil
}

// linkNoReplace publishes from as to within dirfd. linkat never replaces an
// existing entry (EEXIST), so publishing cannot overwrite any file.
func linkNoReplace(dirfd int, from, to string) (exists bool, err error) {
	err = unix.Linkat(dirfd, from, dirfd, to, 0)
	if errors.Is(err, unix.EEXIST) {
		return true, nil
	}
	if err != nil {
		return false, errUnavailable
	}
	return false, nil
}

func unlinkAt(dirfd int, name string) { _ = unix.Unlinkat(dirfd, name, 0) }

// shaAt hashes a regular, single-link, non-symlink file in dirfd.
func shaAt(dirfd int, name string) (string, error) {
	f, _, err := openLeaf(dirfd, name, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, MaxUploadBytesCeiling+1)); err != nil {
		return "", errUnavailable
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readManifestNoFollow reads .artifacts/manifest.json through the same
// no-follow chain, so a symlinked .artifacts or manifest cannot make the
// bridge index another directory. A missing manifest is an empty one.
func readManifestNoFollow(worktree string) ([]byte, error) {
	dirfd, err := openDirChain(worktree, []string{artifactsDirName}, false)
	if errors.Is(err, errNotExist) {
		return nil, errNotExist
	}
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirfd)
	f, fi, err := openLeaf(dirfd, "manifest.json", "manifest.json")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi.Size() > 8<<20 {
		return nil, errUnavailable
	}
	return io.ReadAll(io.LimitReader(f, 8<<20))
}
