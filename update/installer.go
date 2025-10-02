package update

import (
	"fmt"
	"strings"

	"github.com/blang/semver"
	"github.com/jfox85/devx/version"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

// PerformUpdate downloads and installs the latest version
func PerformUpdate(force bool) error {
	// Parse current version - handle dev versions gracefully
	var currentVersion semver.Version
	rawVersion := strings.TrimPrefix(version.Version, "v")

	if parsedVersion, err := semver.Parse(rawVersion); err != nil {
		// For dev versions, use 0.0.0 so any release is considered newer
		currentVersion = semver.Version{Major: 0, Minor: 0, Patch: 0}
	} else {
		currentVersion = parsedVersion
	}

	// Check for latest version
	latest, found, err := selfupdate.DetectLatest(GitHubRepo)
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	if !found {
		return fmt.Errorf("no release information found")
	}

	// Check if update is needed
	if latest.Version.LTE(currentVersion) && !force {
		return fmt.Errorf("you are already running the latest version (%s)", version.Version)
	}

	// Perform the update
	release, err := selfupdate.UpdateSelf(currentVersion, GitHubRepo)
	if err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("✅ Successfully updated to %s!\n", release.Version)
	fmt.Println("🎉 devx has been updated. Restart any running instances to use the new version.")

	// Show release notes if available
	if release.ReleaseNotes != "" {
		fmt.Println("\n📋 Release Notes:")
		fmt.Println(release.ReleaseNotes)
	}

	return nil
}

// UpdateAvailable checks if an update is available without downloading
func UpdateAvailable() (bool, string, error) {
	info, err := CheckForUpdates()
	if err != nil {
		return false, "", err
	}

	return info.Available, info.LatestVersion, nil
}
