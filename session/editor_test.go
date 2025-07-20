package session

import (
	"os"
	"os/exec"
	"testing"

	"github.com/spf13/viper"
)

func TestGetEditorCommand(t *testing.T) {
	// Save original environment
	originalVisual := os.Getenv("VISUAL")
	originalEditor := os.Getenv("EDITOR")
	
	// Clear environment and viper for clean test
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	viper.Set("editor", "")
	
	defer func() {
		// Restore original environment
		if originalVisual != "" {
			os.Setenv("VISUAL", originalVisual)
		}
		if originalEditor != "" {
			os.Setenv("EDITOR", originalEditor)
		}
	}()
	
	// Test 1: No editor configured
	result := GetEditorCommand()
	if result != "" {
		t.Errorf("Expected empty string when no editor configured, got '%s'", result)
	}
	
	// Test 2: EDITOR environment variable
	os.Setenv("EDITOR", "nano")
	result = GetEditorCommand()
	if result != "nano" {
		t.Errorf("Expected 'nano' from EDITOR env var, got '%s'", result)
	}
	
	// Test 3: VISUAL environment variable (should override EDITOR)
	os.Setenv("VISUAL", "vim")
	result = GetEditorCommand()
	if result != "vim" {
		t.Errorf("Expected 'vim' from VISUAL env var, got '%s'", result)
	}
	
	// Test 4: devx config setting (should override everything)
	viper.Set("editor", "cursor")
	result = GetEditorCommand()
	if result != "cursor" {
		t.Errorf("Expected 'cursor' from devx config, got '%s'", result)
	}
}

func TestIsEditorAvailable(t *testing.T) {
	// Save original settings
	originalEditor := viper.GetString("editor")
	originalVisual := os.Getenv("VISUAL")
	originalEditorEnv := os.Getenv("EDITOR")
	
	defer func() {
		viper.Set("editor", originalEditor)
		if originalVisual != "" {
			os.Setenv("VISUAL", originalVisual)
		} else {
			os.Unsetenv("VISUAL")
		}
		if originalEditorEnv != "" {
			os.Setenv("EDITOR", originalEditorEnv)
		} else {
			os.Unsetenv("EDITOR")
		}
	}()
	
	// Test with a command that should exist (echo)
	viper.Set("editor", "echo")
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	if !IsEditorAvailable() {
		t.Error("Expected 'echo' command to be available")
	}
	
	// Test with a command that shouldn't exist
	viper.Set("editor", "nonexistent-editor-command-12345")
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	if IsEditorAvailable() {
		t.Error("Expected nonexistent command to be unavailable")
	}
	
	// Test with no editor configured
	viper.Set("editor", "")
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	if IsEditorAvailable() {
		t.Error("Expected no editor available when none configured")
	}
}

// TestCursorExec tests if the cursor command is available (as per requirements)
func TestCursorExec(t *testing.T) {
	err := exec.Command("cursor", "--version").Run()
	if err != nil {
		t.Skipf("cursor command not available: %v", err)
	}
}

func TestLaunchEditor(t *testing.T) {
	// Save original viper setting and environment
	originalEditor := viper.GetString("editor")
	originalVisual := os.Getenv("VISUAL")
	originalEditorEnv := os.Getenv("EDITOR")
	
	defer func() {
		viper.Set("editor", originalEditor)
		if originalVisual != "" {
			os.Setenv("VISUAL", originalVisual)
		} else {
			os.Unsetenv("VISUAL")
		}
		if originalEditorEnv != "" {
			os.Setenv("EDITOR", originalEditorEnv)
		} else {
			os.Unsetenv("EDITOR")
		}
	}()
	
	// Clear all editor settings
	viper.Set("editor", "")
	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	
	// Test with no editor configured (should return PID 0 and no error)
	pid, err := LaunchEditor("/tmp")
	if err != nil {
		t.Errorf("LaunchEditor should not error when no editor configured, got: %v", err)
	}
	if pid != 0 {
		t.Errorf("Expected PID 0 when no editor configured, got %d", pid)
	}
	
	// Test with a safe command that won't actually open an editor
	viper.Set("editor", "echo")
	pid, err = LaunchEditor("/tmp")
	if err != nil {
		t.Errorf("LaunchEditor failed with echo command: %v", err)
	}
	if pid <= 0 {
		t.Errorf("Expected positive PID for echo command, got %d", pid)
	}
}

func TestIsProcessRunning(t *testing.T) {
	// Test with invalid PIDs
	if IsProcessRunning(0) {
		t.Error("PID 0 should not be considered running")
	}
	
	if IsProcessRunning(-1) {
		t.Error("Negative PID should not be considered running")
	}
	
	// Test with our own process (should be running)
	myPid := os.Getpid()
	if !IsProcessRunning(myPid) {
		t.Errorf("Our own process PID %d should be running", myPid)
	}
	
	// Test with a non-existent PID (very high number)
	if IsProcessRunning(999999) {
		t.Error("Very high PID should not be running")
	}
}