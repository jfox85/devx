package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	
	"github.com/spf13/viper"
)

// CopyBootstrapFiles copies configured bootstrap files from project root to the worktree
func CopyBootstrapFiles(projectRoot, worktreePath string) error {
	bootstrapFiles := viper.GetStringSlice("bootstrap_files")
	if len(bootstrapFiles) == 0 {
		return nil // No bootstrap files configured
	}
	
	fmt.Printf("Copying bootstrap files...\n")
	
	for _, relPath := range bootstrapFiles {
		// Clean and validate the path
		relPath = strings.TrimSpace(relPath)
		if relPath == "" {
			continue
		}
		
		// Ensure the path is relative and doesn't escape the project root
		if filepath.IsAbs(relPath) {
			return fmt.Errorf("bootstrap file path must be relative: %s", relPath)
		}
		
		// Clean the path to prevent directory traversal
		cleanPath := filepath.Clean(relPath)
		if strings.HasPrefix(cleanPath, "..") {
			return fmt.Errorf("bootstrap file path cannot escape project root: %s", relPath)
		}
		
		sourcePath := filepath.Join(projectRoot, cleanPath)
		destPath := filepath.Join(worktreePath, cleanPath)
		
		// Check if source file exists
		if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
			fmt.Printf("  Warning: Bootstrap file not found: %s\n", relPath)
			continue
		}
		
		// Copy the file
		if err := copyFile(sourcePath, destPath); err != nil {
			return fmt.Errorf("failed to copy bootstrap file %s: %w", relPath, err)
		}
		
		fmt.Printf("  Copied: %s\n", relPath)
	}
	
	return nil
}

// copyFile copies a file from source to destination, creating directories as needed
func copyFile(sourcePath, destPath string) error {
	// Read source file
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}
	
	// Create destination directory if it doesn't exist
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}
	
	// Get source file permissions
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to get source file info: %w", err)
	}
	
	// Write destination file with same permissions
	if err := os.WriteFile(destPath, sourceData, sourceInfo.Mode()); err != nil {
		return fmt.Errorf("failed to write destination file: %w", err)
	}
	
	return nil
}

// GetProjectRoot finds the git repository root directory
func GetProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	
	// Walk up the directory tree looking for .git directory
	currentPath := cwd
	for {
		gitPath := filepath.Join(currentPath, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.IsDir() {
			return currentPath, nil
		}
		
		// Get parent directory
		parentPath := filepath.Dir(currentPath)
		
		// If we've reached the root directory, stop searching
		if parentPath == currentPath {
			break
		}
		
		currentPath = parentPath
	}
	
	return "", fmt.Errorf("not in a git repository")
}