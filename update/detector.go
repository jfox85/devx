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

// isHomebrewManaged checks if a binary is managed by Homebrew
func isHomebrewManaged(path string) bool {
	// Check for Cellar paths (direct installation)
	if strings.Contains(path, "/usr/local/Cellar/devx") ||
		strings.Contains(path, "/opt/homebrew/Cellar/devx") {
		return true
	}

	// Check if it's a symlink to a Homebrew Cellar location
	if link, err := os.Readlink(path); err == nil {
		return strings.Contains(link, "/Cellar/devx")
	}

	// Check common Homebrew bin locations
	if strings.Contains(path, "/usr/local/bin/devx") ||
		strings.Contains(path, "/opt/homebrew/bin/devx") {
		// Verify it's actually managed by Homebrew via symlink check
		if link, err := os.Readlink(path); err == nil {
			return strings.Contains(link, "/Cellar/")
		}
	}

	return false
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
