//go:build !darwin && !linux

package main

// openNoFollow is 0 on platforms without an O_NOFOLLOW open flag (e.g. Windows),
// so the desktop file-drop path still compiles there. The leaf-symlink defense
// is a no-op on those targets; the regular-file and size checks still apply.
const openNoFollow = 0
