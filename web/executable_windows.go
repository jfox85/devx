//go:build windows

package web

import "os"

// cliExecutableName is the file name of the devx CLI binary on this platform.
const cliExecutableName = "devx.exe"

// isExecutableFile reports whether a regular file is runnable. Windows has no
// execute permission bits (os.FileMode always reports 0666/0444); runnability
// comes from the .exe extension, which cliExecutableName already guarantees.
func isExecutableFile(_ string, _ os.FileInfo) bool {
	return true
}
