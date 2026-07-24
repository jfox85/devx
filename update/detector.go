package update

import (
	"os"
	"path/filepath"
	"strings"
)

// InstallMethod represents how devx was installed
type InstallMethod string

const (
	InstallMethodHomebrew  InstallMethod = "homebrew"
	InstallMethodGoInstall InstallMethod = "go-install"
	InstallMethodManual    InstallMethod = "manual"
	InstallMethodUnknown   InstallMethod = "unknown"
)

// DetectInstallMethod tries to determine how devx was installed
func DetectInstallMethod() InstallMethod {
	exe, err := os.Executable()
	if err != nil {
		return InstallMethodUnknown
	}

	exePath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		exePath = exe
	}

	// Check for Homebrew installation
	if isHomebrewManaged(exePath) {
		return InstallMethodHomebrew
	}

	// Check for go install
	if strings.Contains(exePath, "/go/bin/") {
		return InstallMethodGoInstall
	}

	// Default to manual installation
	return InstallMethodManual
}

// isHomebrewManaged checks if a binary is managed by Homebrew.
func isHomebrewManaged(path string) bool {
	// Direct Cellar installation, e.g. /opt/homebrew/Cellar/devx/1.2.3/bin/devx.
	if isDevxCellarPath(path) {
		return true
	}

	// Homebrew symlinks bin entries (/usr/local/bin, /opt/homebrew/bin) into the
	// Cellar. If this path is such a symlink, resolve it and check whether the
	// target is the devx Cellar package. A symlink into the Cellar is a Homebrew
	// install regardless of where the symlink itself lives.
	if link, err := os.Readlink(path); err == nil {
		return isDevxCellarPath(link)
	}

	return false
}

// isDevxCellarPath reports whether path points at the "devx" package inside a
// Homebrew Cellar directory. The package name must occupy a full path component
// so sibling packages like "devx-tools" don't collide with "devx" (a plain
// substring match would accept /opt/homebrew/Cellar/devx-tools/...).
func isDevxCellarPath(path string) bool {
	const marker = "/Cellar/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return false
	}
	rest := path[idx+len(marker):]
	return rest == "devx" || strings.HasPrefix(rest, "devx/")
}

// CanSelfUpdate returns true if the installation method supports self-update
func CanSelfUpdate() bool {
	method := DetectInstallMethod()
	// Homebrew installations should use `brew upgrade`
	return method != InstallMethodHomebrew
}

// GetUpdateInstructions returns platform-specific update instructions
func GetUpdateInstructions() string {
	method := DetectInstallMethod()

	switch method {
	case InstallMethodHomebrew:
		return "Please update using Homebrew:\n  brew upgrade devx\n\nOr:\n  brew update && brew upgrade devx"
	case InstallMethodGoInstall:
		return "Please update using go install:\n  go install github.com/jfox85/devx@latest"
	case InstallMethodManual:
		return "Run 'devx update' to update to the latest version"
	default:
		return "Unable to determine installation method. Please reinstall devx."
	}
}
