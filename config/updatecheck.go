package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// UpdateCheckState stores the last update check information
type UpdateCheckState struct {
	LastCheck           time.Time `json:"last_check"`
	LastNotifiedVersion string    `json:"last_notified_version,omitempty"`
}

// GetUpdateCheckPath returns the path to the update check state file
func GetUpdateCheckPath() (string, error) {
	configDir := GetConfigDir()
	return filepath.Join(configDir, "updatecheck.json"), nil
}

// LoadUpdateCheckState loads the update check state from disk
func LoadUpdateCheckState() (*UpdateCheckState, error) {
	path, err := GetUpdateCheckPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty state if file doesn't exist
			return &UpdateCheckState{}, nil
		}
		return nil, err
	}

	var state UpdateCheckState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// SaveUpdateCheckState saves the update check state to disk
func SaveUpdateCheckState(state *UpdateCheckState) error {
	path, err := GetUpdateCheckPath()
	if err != nil {
		return err
	}

	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// ShouldCheckForUpdates determines if we should check for updates based on interval
func ShouldCheckForUpdates(lastCheck time.Time, interval time.Duration) bool {
	if lastCheck.IsZero() {
		return true
	}
	return time.Since(lastCheck) >= interval
}
