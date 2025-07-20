package config

import (
	"os"
	"path/filepath"
)

// FindProjectConfigDir searches for a .devx directory starting from the current
// working directory and walking up the directory tree. Returns the path to the
// .devx directory if found, or empty string if not found.
func FindProjectConfigDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	return findProjectConfigDirFromPath(cwd)
}

// findProjectConfigDirFromPath searches for a .devx directory starting from the
// given path and walking up the directory tree.
func findProjectConfigDirFromPath(startPath string) string {
	currentPath := startPath
	
	for {
		devxPath := filepath.Join(currentPath, ".devx")
		
		// Check if .devx directory exists
		if info, err := os.Stat(devxPath); err == nil && info.IsDir() {
			return devxPath
		}
		
		// Get parent directory
		parentPath := filepath.Dir(currentPath)
		
		// If we've reached the root directory, stop searching
		if parentPath == currentPath {
			break
		}
		
		currentPath = parentPath
	}
	
	return ""
}

// GetConfigPath returns the path to the config file, checking project-level first
func GetConfigPath() string {
	// Check for project-level config first
	if projectDir := FindProjectConfigDir(); projectDir != "" {
		configPath := filepath.Join(projectDir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}
	
	// Fall back to global config
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	
	return filepath.Join(home, ".config", "devx", "config.yaml")
}

// GetSessionsPath returns the path to the sessions.json file, checking project-level first
func GetSessionsPath() string {
	// Check for project-level sessions first
	if projectDir := FindProjectConfigDir(); projectDir != "" {
		sessionsPath := filepath.Join(projectDir, "sessions.json")
		if _, err := os.Stat(sessionsPath); err == nil {
			return sessionsPath
		}
	}
	
	// Fall back to global sessions
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	
	return filepath.Join(home, ".config", "devx", "sessions.json")
}

// GetTmuxTemplatePath returns the path to the session.yaml.tmpl file, checking project-level first
func GetTmuxTemplatePath() string {
	// Check for project-level template first
	if projectDir := FindProjectConfigDir(); projectDir != "" {
		templatePath := filepath.Join(projectDir, "session.yaml.tmpl")
		if _, err := os.Stat(templatePath); err == nil {
			return templatePath
		}
	}
	
	// Fall back to global template
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	
	return filepath.Join(home, ".config", "devx", "session.yaml.tmpl")
}

// GetConfigDir returns the config directory path, checking project-level first
func GetConfigDir() string {
	// Check for project-level config first
	if projectDir := FindProjectConfigDir(); projectDir != "" {
		return projectDir
	}
	
	// Fall back to global config
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	
	return filepath.Join(home, ".config", "devx")
}