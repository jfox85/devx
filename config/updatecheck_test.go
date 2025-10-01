package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateCheckStatePersistence(t *testing.T) {
	// Use a temporary directory for testing
	tempDir := t.TempDir()
	originalHome := os.Getenv("HOME")

	// Create a mock config directory
	_ = filepath.Join(tempDir, ".config", "devx")
	os.Setenv("HOME", tempDir)
	defer os.Setenv("HOME", originalHome)

	// Change working directory to temp dir so FindProjectConfigDir doesn't find the real .devx
	originalWd, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore directory: %v", err)
		}
	}()

	// Test saving and loading
	state := &UpdateCheckState{
		LastCheck:           time.Now().Truncate(time.Second), // Truncate for comparison
		LastNotifiedVersion: "0.2.0",
	}

	err := SaveUpdateCheckState(state)
	if err != nil {
		t.Fatalf("Failed to save update check state: %v", err)
	}

	loaded, err := LoadUpdateCheckState()
	if err != nil {
		t.Fatalf("Failed to load update check state: %v", err)
	}

	if !loaded.LastCheck.Equal(state.LastCheck) {
		t.Errorf("LastCheck mismatch: got %v, want %v", loaded.LastCheck, state.LastCheck)
	}

	if loaded.LastNotifiedVersion != state.LastNotifiedVersion {
		t.Errorf("LastNotifiedVersion mismatch: got %v, want %v", loaded.LastNotifiedVersion, state.LastNotifiedVersion)
	}
}

func TestShouldCheckForUpdates(t *testing.T) {
	tests := []struct {
		name      string
		lastCheck time.Time
		interval  time.Duration
		expected  bool
	}{
		{
			name:      "never checked before (zero time)",
			lastCheck: time.Time{},
			interval:  24 * time.Hour,
			expected:  true,
		},
		{
			name:      "checked 1 hour ago, interval 24 hours",
			lastCheck: time.Now().Add(-1 * time.Hour),
			interval:  24 * time.Hour,
			expected:  false,
		},
		{
			name:      "checked 25 hours ago, interval 24 hours",
			lastCheck: time.Now().Add(-25 * time.Hour),
			interval:  24 * time.Hour,
			expected:  true,
		},
		{
			name:      "checked just now, interval 1 hour",
			lastCheck: time.Now(),
			interval:  1 * time.Hour,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldCheckForUpdates(tt.lastCheck, tt.interval)
			if result != tt.expected {
				t.Errorf("ShouldCheckForUpdates(%v, %v) = %v, want %v",
					tt.lastCheck, tt.interval, result, tt.expected)
			}
		})
	}
}

func TestLoadUpdateCheckStateNonExistent(t *testing.T) {
	// Use a completely isolated temporary directory
	tempDir := t.TempDir()

	// Create a unique test directory to avoid conflicts with other tests
	testConfigDir := filepath.Join(tempDir, "test-config-nonexistent")

	// Override the GetConfigDir function for this test by using environment
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", testConfigDir)
	defer os.Setenv("HOME", originalHome)

	// Change working directory to temp dir so FindProjectConfigDir doesn't find the real .devx
	originalWd, _ := os.Getwd()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalWd); err != nil {
			t.Errorf("Failed to restore directory: %v", err)
		}
	}()

	// Should return empty state for non-existent file
	state, err := LoadUpdateCheckState()
	if err != nil {
		t.Fatalf("Expected no error for non-existent file, got: %v", err)
	}

	if !state.LastCheck.IsZero() {
		t.Errorf("Expected zero LastCheck for new state, got: %v", state.LastCheck)
	}

	if state.LastNotifiedVersion != "" {
		t.Errorf("Expected empty LastNotifiedVersion for new state, got: %v", state.LastNotifiedVersion)
	}
}
