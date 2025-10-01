package update

import (
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/version"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

const (
	// DefaultCheckInterval is the default time between update checks
	DefaultCheckInterval = 24 * time.Hour

	// GitHubRepo is the repository to check for updates
	GitHubRepo = "jfox85/devx"
)

// UpdateInfo contains information about an available update
type UpdateInfo struct {
	CurrentVersion string
	LatestVersion  string
	ReleaseNotes   string
	ReleaseURL     string
	Available      bool
}

// CheckForUpdates checks GitHub for a newer version
func CheckForUpdates() (*UpdateInfo, error) {
	// Parse current version - handle dev versions gracefully
	var currentVersion semver.Version
	cleanVersion := strings.TrimPrefix(version.Version, "v")

	// Check if this is a dev version (git commit hash, etc.)
	if parsedVersion, err := semver.Parse(cleanVersion); err != nil {
		// For dev versions, use 0.0.0 so any release is considered newer
		currentVersion = semver.Version{Major: 0, Minor: 0, Patch: 0}
	} else {
		currentVersion = parsedVersion
	}

	// Check GitHub for latest release
	latest, found, err := selfupdate.DetectLatest(GitHubRepo)
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}

	if !found {
		return nil, fmt.Errorf("no release information found")
	}

	info := &UpdateInfo{
		CurrentVersion: version.Version, // Use actual version string for display
		LatestVersion:  latest.Version.String(),
		ReleaseNotes:   latest.ReleaseNotes,
		ReleaseURL:     latest.URL,
		Available:      latest.Version.GT(currentVersion),
	}

	return info, nil
}

// CheckForUpdatesWithCache checks for updates if the check interval has passed
func CheckForUpdatesWithCache(interval time.Duration) (*UpdateInfo, bool, error) {
	// Load last check state
	state, err := config.LoadUpdateCheckState()
	if err != nil {
		return nil, false, fmt.Errorf("loading update check state: %w", err)
	}

	// Check if we should perform the update check
	if !config.ShouldCheckForUpdates(state.LastCheck, interval) {
		return nil, false, nil // Not time to check yet
	}

	// Perform the check
	info, err := CheckForUpdates()
	if err != nil {
		return nil, false, err
	}

	// Save the check time
	state.LastCheck = time.Now()
	if info.Available {
		state.LastNotifiedVersion = info.LatestVersion
	}
	if err := config.SaveUpdateCheckState(state); err != nil {
		// Log but don't fail - this is not critical
		fmt.Printf("Warning: failed to save update check state: %v\n", err)
	}

	return info, true, nil
}

// ShouldNotifyUser determines if we should notify the user about an update
// Returns true if:
// - An update is available
// - We haven't already notified about this version
func ShouldNotifyUser(info *UpdateInfo) (bool, error) {
	if !info.Available {
		return false, nil
	}

	state, err := config.LoadUpdateCheckState()
	if err != nil {
		return true, nil // On error, default to notifying
	}

	// If we haven't notified about this version yet, notify
	return state.LastNotifiedVersion != info.LatestVersion, nil
}
