//go:build !windows

package web

import "os"

// cliExecutableName is the file name of the devx CLI binary on this platform.
const cliExecutableName = "devx"

// isExecutableFile reports whether info has any execute permission bit set.
func isExecutableFile(_ string, info os.FileInfo) bool {
	return info.Mode().Perm()&0o111 != 0
}
