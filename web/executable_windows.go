//go:build windows

package web

import (
	"os"
	"path/filepath"
	"strings"
)

// cliExecutableName is the file name of the devx CLI binary on this platform.
const cliExecutableName = "devx.exe"

// isExecutableFile reports whether path is runnable. Windows has no execute
// permission bits (os.FileMode always reports 0666/0444), so executability is
// decided by extension, matching how exec.LookPath resolves commands.
func isExecutableFile(path string, _ os.FileInfo) bool {
	return strings.EqualFold(filepath.Ext(path), ".exe")
}
