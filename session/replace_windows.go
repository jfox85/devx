//go:build windows

package session

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileRenameInfo mirrors Win32 FILE_RENAME_INFO as used with
// FileRenameInfoEx: a Flags word in place of ReplaceIfExists, followed by a
// variable-length file name.
type fileRenameInfo struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

// replaceFile atomically publishes src at dst, even while other processes
// hold dst open (the Unix rename(2) behaviour the store relies on).
//
// os.Rename on Windows is MoveFileEx(MOVEFILE_REPLACE_EXISTING), which fails
// with "Access is denied" whenever any handle to dst is open, so a lock-free
// reader made a concurrent writer lose its update. A rename with
// FILE_RENAME_FLAG_POSIX_SEMANTICS|REPLACE_IF_EXISTS replaces dst under open
// handles that allow FILE_SHARE_DELETE (see openSessionsFileForRead); those
// readers keep the old contents. Filesystems without POSIX rename support
// (e.g. FAT, some network shares) fall back to os.Rename.
func replaceFile(src, dst string) error {
	if err := posixReplace(src, dst); err == nil {
		return nil
	}
	return os.Rename(src, dst)
}

func posixReplace(src, dst string) error {
	srcp, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(srcp, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)

	name, err := windows.UTF16FromString(dst)
	if err != nil {
		return err
	}
	nameBytes := (len(name) - 1) * 2 // without the terminating NUL
	var probe fileRenameInfo
	size := int(unsafe.Offsetof(probe.FileName)) + nameBytes + 2
	buf := make([]byte, size)
	info := (*fileRenameInfo)(unsafe.Pointer(&buf[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(size))
}
