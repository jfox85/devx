//go:build darwin || linux

package main

import "syscall"

// openNoFollow makes os.OpenFile refuse a symlinked final path component, so a
// dropped ".png" symlink is rejected (ELOOP) instead of dereferenced. Defined
// per-platform because syscall.O_NOFOLLOW is not available on Windows.
const openNoFollow = syscall.O_NOFOLLOW
