package artifact

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jfox85/devx/session"
)

// ResolveFile returns the absolute on-disk path of the artifact with the given
// ID. The path is validated against the manifest and rejected if any component
// is a symlink, so callers (the desktop open/save bridge) can never be pointed
// at a file outside the session's artifact directory.
func ResolveFile(sess *session.Session, id string) (string, *Artifact, error) {
	if sess == nil {
		return "", nil, fmt.Errorf("session is required")
	}
	if strings.TrimSpace(id) == "" {
		return "", nil, fmt.Errorf("artifact id is required")
	}
	manifest, err := LoadManifest(sess)
	if err != nil {
		return "", nil, err
	}
	a, _ := Find(manifest, id)
	if a == nil {
		return "", nil, fmt.Errorf("artifact %q not found", id)
	}
	abs, err := SecureExistingPath(DirForSession(sess), a.File)
	if err != nil {
		return "", nil, fmt.Errorf("artifact file unavailable: %w", err)
	}
	return abs, a, nil
}

// safeOpenExts lists document/media formats that are safe to hand to the OS
// "open with default app" handler. Anything else (scripts, .command, .app,
// archives, binaries) could execute code when opened, so callers should reveal
// the file in the file manager instead.
//
// HTML and SVG are deliberately excluded. Artifact content is agent-written,
// and the default app for these is a browser, which would run their scripts
// as a local file:// page outside the sandboxed in-app preview (served with
// CSP sandbox). They are viewed in-app and revealed in the file manager here.
var safeOpenExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".mp4": true, ".mov": true, ".webm": true,
	".pdf": true,
	".md":  true, ".txt": true, ".log": true, ".diff": true, ".patch": true,
	".json": true, ".csv": true,
}

// SafeToOpenExternally reports whether a file can be opened with the OS default
// application without risking code execution.
func SafeToOpenExternally(file string) bool {
	return safeOpenExts[strings.ToLower(filepath.Ext(file))]
}
