//go:build !(darwin || linux)

package artifactbridge

import (
	"os"
	"path/filepath"

	artifactpkg "github.com/jfox85/devx/artifact"
)

func fileIdentity(os.FileInfo) string { return "" }

// openUnderArtifacts on platforms without openat/O_NOFOLLOW: validate every
// component with Lstat, open, then confirm the opened file is the same file
// the validated path named. Weaker against concurrent replacement than the
// unix implementation; DevX's MCP bridge is supported on macOS/Linux.
func openUnderArtifacts(worktree, rel string) (*os.File, os.FileInfo, error) {
	if _, err := splitRel(rel); err != nil {
		return nil, nil, err
	}
	base := filepath.Join(worktree, artifactsDirName)
	abs, err := artifactpkg.SecureExistingPath(base, filepath.FromSlash(rel))
	if err != nil {
		return nil, nil, errUnavailable
	}
	before, err := os.Lstat(abs)
	if err != nil || !before.Mode().IsRegular() {
		return nil, nil, errUnavailable
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, errUnavailable
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		_ = f.Close()
		return nil, nil, errUnavailable
	}
	return f, info, nil
}
