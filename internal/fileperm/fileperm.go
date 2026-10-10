// Package fileperm centralizes Unix permission-bit checks so they are applied
// only where os.FileMode permission bits reflect real access control.
package fileperm

import (
	"os"
	"runtime"
)

// ModeBitsEnforced reports whether os.FileMode permission bits describe real
// access control on this platform. On Windows they do not: Go synthesizes
// 0666/0444 for files and 0777 for directories from the read-only attribute,
// and access is governed by ACLs that FileMode cannot express.
const ModeBitsEnforced = runtime.GOOS != "windows"

// AccessibleByOthers reports whether mode grants any of the group/other bits in
// mask (e.g. 0o077 for "readable or writable by others", 0o022 for "writable by
// others"). It always returns false where mode bits are not enforced, so
// callers must not rely on it as the sole protection on such platforms.
func AccessibleByOthers(mode os.FileMode, mask os.FileMode) bool {
	return ModeBitsEnforced && mode.Perm()&mask != 0
}
