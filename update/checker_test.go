package update

import (
	"os"
	"testing"
	"time"

	"github.com/jfox85/devx/config"
)

func TestShouldNotifyUser(t *testing.T) {
	tests := []struct {
		name         string
		info         *UpdateInfo
		lastNotified string
		expected     bool
		description  string
	}{
		{
			name: "new update available, never notified",
			info: &UpdateInfo{
				CurrentVersion: "0.1.0",
				LatestVersion:  "0.2.0",
				Available:      true,
			},
			lastNotified: "",
			expected:     true,
			description:  "Should notify when update is available and never notified",
		},
		{
			name: "same update already notified",
			info: &UpdateInfo{
				CurrentVersion: "0.1.0",
				LatestVersion:  "0.2.0",
				Available:      true,
			},
			lastNotified: "0.2.0",
			expected:     false,
			description:  "Should not notify if already notified about this version",
		},
		{
			name: "newer update available than last notification",
			info: &UpdateInfo{
				CurrentVersion: "0.1.0",
				LatestVersion:  "0.3.0",
				Available:      true,
			},
			lastNotified: "0.2.0",
			expected:     true,
			description:  "Should notify if a newer version is available than last notified",
		},
		{
			name: "no update available",
			info: &UpdateInfo{
				CurrentVersion: "0.2.0",
				LatestVersion:  "0.2.0",
				Available:      false,
			},
			lastNotified: "",
			expected:     false,
			description:  "Should not notify when no update is available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use temp directory to isolate tests
			tempDir := t.TempDir()
			originalHome := os.Getenv("HOME")
			os.Setenv("HOME", tempDir)
			defer os.Setenv("HOME", originalHome)

			originalWd, _ := os.Getwd()
			os.Chdir(tempDir)
			defer os.Chdir(originalWd)

			// Setup: Save a test state
			state := &config.UpdateCheckState{
				LastCheck:           time.Now(),
				LastNotifiedVersion: tt.lastNotified,
			}
			if err := config.SaveUpdateCheckState(state); err != nil {
				t.Fatalf("Failed to save test state: %v", err)
			}

			// Test
			result, err := ShouldNotifyUser(tt.info)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("%s: got %v, want %v", tt.description, result, tt.expected)
			}
		})
	}
}
