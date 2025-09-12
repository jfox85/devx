package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ClaudeSettings struct {
	Permissions *Permissions `json:"permissions,omitempty"`
	Hooks       *Hooks       `json:"hooks,omitempty"`
}

type Permissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
	Ask   []string `json:"ask,omitempty"`
}

type Hooks struct {
	Stop         []HookGroup `json:"Stop,omitempty"`
	Notification []HookGroup `json:"Notification,omitempty"`
}

type HookGroup struct {
	Hooks []Hook `json:"hooks"`
}

type Hook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type InstallResult struct {
	Created       bool
	Updated       bool
	AlreadyExists bool
	BackupCreated bool
	BackupPath    string
	Message       string
}

const (
	stopHookCommand         = "devx session flag --force $SESSION_NAME 'Claude Done'"
	notificationHookCommand = "devx session flag --force $SESSION_NAME 'Claude is waiting for your input'"
)

func InstallHooks(projectPath string, force bool, createBackup bool) (*InstallResult, error) {
	claudeDir := filepath.Join(projectPath, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.local.json")

	result := &InstallResult{}

	// Check if .claude directory exists
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		// Create .claude directory
		if err := os.MkdirAll(claudeDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create .claude directory: %w", err)
		}
		result.Created = true
	}

	// Check if settings file exists
	var existingSettings *ClaudeSettings
	if _, err := os.Stat(settingsPath); err == nil {
		// File exists, read it
		data, err := os.ReadFile(settingsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read existing settings: %w", err)
		}

		existingSettings = &ClaudeSettings{}
		if err := json.Unmarshal(data, existingSettings); err != nil {
			return nil, fmt.Errorf("failed to parse existing settings: %w", err)
		}

		// Check if hooks already exist and match our expected commands
		if existingSettings.Hooks != nil && hooksMatch(existingSettings.Hooks) {
			if !force {
				result.AlreadyExists = true
				result.Message = "Claude hooks are already installed and match expected configuration"
				return result, nil
			}
		}

		// Create backup if requested
		if createBackup {
			backupPath := fmt.Sprintf("%s.backup.%d", settingsPath, time.Now().Unix())
			if err := copyFile(settingsPath, backupPath); err != nil {
				return nil, fmt.Errorf("failed to create backup: %w", err)
			}
			result.BackupCreated = true
			result.BackupPath = backupPath
		}

		result.Updated = true
	} else {
		// File doesn't exist
		existingSettings = &ClaudeSettings{}
		result.Created = true
	}

	// Ensure hooks structure exists
	if existingSettings.Hooks == nil {
		existingSettings.Hooks = &Hooks{}
	}

	// Set the hooks
	existingSettings.Hooks.Stop = []HookGroup{
		{
			Hooks: []Hook{
				{
					Type:    "command",
					Command: stopHookCommand,
				},
			},
		},
	}

	existingSettings.Hooks.Notification = []HookGroup{
		{
			Hooks: []Hook{
				{
					Type:    "command",
					Command: notificationHookCommand,
				},
			},
		},
	}

	// Write the updated settings
	data, err := json.MarshalIndent(existingSettings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write atomically
	tempPath := settingsPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempPath, settingsPath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return nil, fmt.Errorf("failed to rename temp file: %w", err)
	}

	if result.Created {
		result.Message = "Claude hooks installed successfully"
	} else if result.Updated {
		result.Message = "Claude hooks updated successfully"
	}

	return result, nil
}

func CheckHooksStatus(projectPath string) (bool, error) {
	settingsPath := filepath.Join(projectPath, ".claude", "settings.local.json")

	// Check if settings file exists
	if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		return false, nil
	}

	// Read and parse the settings
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return false, fmt.Errorf("failed to read settings file: %w", err)
	}

	var settings ClaudeSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, fmt.Errorf("failed to parse settings file: %w", err)
	}

	// Check if hooks match our expected configuration
	return settings.Hooks != nil && hooksMatch(settings.Hooks), nil
}

func hooksMatch(hooks *Hooks) bool {
	// Check Stop hooks
	if len(hooks.Stop) != 1 || len(hooks.Stop[0].Hooks) != 1 {
		return false
	}
	stopHook := hooks.Stop[0].Hooks[0]
	if stopHook.Type != "command" || !strings.Contains(stopHook.Command, "Claude Done") {
		return false
	}

	// Check Notification hooks
	if len(hooks.Notification) != 1 || len(hooks.Notification[0].Hooks) != 1 {
		return false
	}
	notificationHook := hooks.Notification[0].Hooks[0]
	if notificationHook.Type != "command" || !strings.Contains(notificationHook.Command, "Claude is waiting for your input") {
		return false
	}

	return true
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func PreviewChanges(projectPath string) (string, error) {
	claudeDir := filepath.Join(projectPath, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.local.json")

	var result strings.Builder

	// Check current state
	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		result.WriteString("Will create: .claude/\n")
		result.WriteString("Will create: .claude/settings.local.json\n\n")
		result.WriteString("New file contents:\n")
	} else if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
		result.WriteString("Will create: .claude/settings.local.json\n\n")
		result.WriteString("New file contents:\n")
	} else {
		result.WriteString("Will update: .claude/settings.local.json\n")
		result.WriteString("Will create: backup file\n\n")
		result.WriteString("Updated file contents:\n")
	}

	// Show what the file will contain
	settings := &ClaudeSettings{
		Hooks: &Hooks{
			Stop: []HookGroup{
				{
					Hooks: []Hook{
						{
							Type:    "command",
							Command: stopHookCommand,
						},
					},
				},
			},
			Notification: []HookGroup{
				{
					Hooks: []Hook{
						{
							Type:    "command",
							Command: notificationHookCommand,
						},
					},
				},
			},
		},
	}

	// If there's existing content, try to merge it
	if _, err := os.Stat(settingsPath); err == nil {
		data, err := os.ReadFile(settingsPath)
		if err == nil {
			var existing ClaudeSettings
			if json.Unmarshal(data, &existing) == nil {
				// Preserve existing permissions
				settings.Permissions = existing.Permissions
			}
		}
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal preview: %w", err)
	}

	result.WriteString("```json\n")
	result.WriteString(string(data))
	result.WriteString("\n```\n")

	return result.String(), nil
}
