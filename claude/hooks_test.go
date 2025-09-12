package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHooks_NewDirectory(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Install hooks in empty directory
	result, err := InstallHooks(tempDir, false, true)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}

	// Check result
	if !result.Created {
		t.Error("Expected Created to be true for new directory")
	}
	if result.Updated {
		t.Error("Expected Updated to be false for new directory")
	}
	if result.AlreadyExists {
		t.Error("Expected AlreadyExists to be false for new directory")
	}

	// Check that .claude directory was created
	claudeDir := filepath.Join(tempDir, ".claude")
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		t.Error(".claude directory was not created")
	}

	// Check that settings file was created
	settingsPath := filepath.Join(claudeDir, "settings.local.json")
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		t.Error("settings.local.json was not created")
	}

	// Verify settings content
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read settings file: %v", err)
	}

	var settings ClaudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("Failed to parse settings JSON: %v", err)
	}

	// Check hooks structure
	if settings.Hooks == nil {
		t.Fatal("Hooks section is nil")
	}

	// Check Stop hooks
	if len(settings.Hooks.Stop) != 1 {
		t.Errorf("Expected 1 Stop hook group, got %d", len(settings.Hooks.Stop))
	}
	if len(settings.Hooks.Stop[0].Hooks) != 1 {
		t.Errorf("Expected 1 Stop hook, got %d", len(settings.Hooks.Stop[0].Hooks))
	}
	stopHook := settings.Hooks.Stop[0].Hooks[0]
	if stopHook.Type != "command" {
		t.Errorf("Expected Stop hook type 'command', got '%s'", stopHook.Type)
	}
	if !strings.Contains(stopHook.Command, "Claude Done") {
		t.Errorf("Expected Stop hook command to contain 'Claude Done', got '%s'", stopHook.Command)
	}

	// Check Notification hooks
	if len(settings.Hooks.Notification) != 1 {
		t.Errorf("Expected 1 Notification hook group, got %d", len(settings.Hooks.Notification))
	}
	if len(settings.Hooks.Notification[0].Hooks) != 1 {
		t.Errorf("Expected 1 Notification hook, got %d", len(settings.Hooks.Notification[0].Hooks))
	}
	notificationHook := settings.Hooks.Notification[0].Hooks[0]
	if notificationHook.Type != "command" {
		t.Errorf("Expected Notification hook type 'command', got '%s'", notificationHook.Type)
	}
	if !strings.Contains(notificationHook.Command, "Claude is waiting for your input") {
		t.Errorf("Expected Notification hook command to contain 'Claude is waiting for your input', got '%s'", notificationHook.Command)
	}
}

func TestInstallHooks_ExistingSettingsWithoutHooks(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create .claude directory and settings file without hooks
	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create .claude dir: %v", err)
	}

	existingSettings := ClaudeSettings{
		Permissions: &Permissions{
			Allow: []string{"Bash(test:*)"},
			Deny:  []string{"Bash(danger:*)"},
		},
	}

	data, err := json.MarshalIndent(existingSettings, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal existing settings: %v", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatalf("Failed to write existing settings: %v", err)
	}

	// Install hooks
	result, err := InstallHooks(tempDir, false, true)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}

	// Check result
	if result.Created {
		t.Error("Expected Created to be false for existing settings")
	}
	if !result.Updated {
		t.Error("Expected Updated to be true for existing settings")
	}
	if !result.BackupCreated {
		t.Error("Expected BackupCreated to be true")
	}

	// Verify backup was created
	if _, err := os.Stat(result.BackupPath); os.IsNotExist(err) {
		t.Error("Backup file was not created")
	}

	// Verify updated settings preserve existing permissions
	updatedData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("Failed to read updated settings: %v", err)
	}

	var updatedSettings ClaudeSettings
	if err := json.Unmarshal(updatedData, &updatedSettings); err != nil {
		t.Fatalf("Failed to parse updated settings: %v", err)
	}

	// Check that permissions were preserved
	if updatedSettings.Permissions == nil {
		t.Error("Permissions were not preserved")
	} else {
		if len(updatedSettings.Permissions.Allow) != 1 || updatedSettings.Permissions.Allow[0] != "Bash(test:*)" {
			t.Error("Allow permissions were not preserved")
		}
		if len(updatedSettings.Permissions.Deny) != 1 || updatedSettings.Permissions.Deny[0] != "Bash(danger:*)" {
			t.Error("Deny permissions were not preserved")
		}
	}

	// Check that hooks were added
	if updatedSettings.Hooks == nil {
		t.Fatal("Hooks were not added")
	}
	if len(updatedSettings.Hooks.Stop) != 1 {
		t.Error("Stop hooks were not added correctly")
	}
	if len(updatedSettings.Hooks.Notification) != 1 {
		t.Error("Notification hooks were not added correctly")
	}
}

func TestInstallHooks_AlreadyInstalled(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// First installation
	result1, err := InstallHooks(tempDir, false, true)
	if err != nil {
		t.Fatalf("First InstallHooks failed: %v", err)
	}
	if !result1.Created {
		t.Error("Expected first installation to create")
	}

	// Second installation (should detect existing hooks)
	result2, err := InstallHooks(tempDir, false, true)
	if err != nil {
		t.Fatalf("Second InstallHooks failed: %v", err)
	}

	// Check result
	if !result2.AlreadyExists {
		t.Error("Expected AlreadyExists to be true for already installed hooks")
	}
	if result2.Created {
		t.Error("Expected Created to be false for already installed hooks")
	}
	if result2.Updated {
		t.Error("Expected Updated to be false for already installed hooks")
	}
	if result2.BackupCreated {
		t.Error("Expected BackupCreated to be false for already installed hooks")
	}
}

func TestInstallHooks_ForceReinstall(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// First installation
	_, err = InstallHooks(tempDir, false, true)
	if err != nil {
		t.Fatalf("First InstallHooks failed: %v", err)
	}

	// Force reinstallation
	result, err := InstallHooks(tempDir, true, true)
	if err != nil {
		t.Fatalf("Force InstallHooks failed: %v", err)
	}

	// Check result
	if result.AlreadyExists {
		t.Error("Expected AlreadyExists to be false when forcing reinstall")
	}
	if result.Created {
		t.Error("Expected Created to be false when force updating existing")
	}
	if !result.Updated {
		t.Error("Expected Updated to be true when force updating")
	}
	if !result.BackupCreated {
		t.Error("Expected BackupCreated to be true when force updating")
	}
}

func TestCheckHooksStatus(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Check status before installation
	installed, err := CheckHooksStatus(tempDir)
	if err != nil {
		t.Fatalf("CheckHooksStatus failed: %v", err)
	}
	if installed {
		t.Error("Expected hooks not to be installed initially")
	}

	// Install hooks
	_, err = InstallHooks(tempDir, false, true)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}

	// Check status after installation
	installed, err = CheckHooksStatus(tempDir)
	if err != nil {
		t.Fatalf("CheckHooksStatus failed after install: %v", err)
	}
	if !installed {
		t.Error("Expected hooks to be installed after InstallHooks")
	}
}

func TestPreviewChanges(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Get preview for new installation
	preview, err := PreviewChanges(tempDir)
	if err != nil {
		t.Fatalf("PreviewChanges failed: %v", err)
	}

	// Check that preview contains expected information
	if !strings.Contains(preview, "Will create: .claude/") {
		t.Error("Preview should mention creating .claude directory")
	}
	if !strings.Contains(preview, "Will create: .claude/settings.local.json") {
		t.Error("Preview should mention creating settings file")
	}
	if !strings.Contains(preview, "```json") {
		t.Error("Preview should contain JSON block")
	}
	if !strings.Contains(preview, "Claude Done") {
		t.Error("Preview should contain Stop hook command")
	}
	if !strings.Contains(preview, "Claude is waiting for your input") {
		t.Error("Preview should contain Notification hook command")
	}
}

func TestInstallHooks_NoBackup(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "claude-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create existing settings
	claudeDir := filepath.Join(tempDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("Failed to create .claude dir: %v", err)
	}

	existingSettings := ClaudeSettings{
		Permissions: &Permissions{
			Allow: []string{"Bash(test:*)"},
		},
	}

	data, err := json.MarshalIndent(existingSettings, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal existing settings: %v", err)
	}

	settingsPath := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(settingsPath, data, 0644); err != nil {
		t.Fatalf("Failed to write existing settings: %v", err)
	}

	// Install hooks without backup
	result, err := InstallHooks(tempDir, false, false)
	if err != nil {
		t.Fatalf("InstallHooks failed: %v", err)
	}

	// Check that no backup was created
	if result.BackupCreated {
		t.Error("Expected BackupCreated to be false when createBackup=false")
	}
	if result.BackupPath != "" {
		t.Error("Expected BackupPath to be empty when createBackup=false")
	}
}
