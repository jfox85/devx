package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectConfigDir(t *testing.T) {
	// Create temp directory structure
	tmpDir, err := os.MkdirTemp("", "devx-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Create nested project structure
	projectRoot := filepath.Join(tmpDir, "myproject")
	subDir := filepath.Join(projectRoot, "src", "components")
	devxDir := filepath.Join(projectRoot, ".devx")
	
	if err := os.MkdirAll(devxDir, 0755); err != nil {
		t.Fatalf("failed to create .devx dir: %v", err)
	}
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}
	
	// Save current directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer os.Chdir(oldWd)
	
	tests := []struct {
		name     string
		startDir string
		want     string
	}{
		{
			name:     "find_from_project_root",
			startDir: projectRoot,
			want:     devxDir,
		},
		{
			name:     "find_from_subdirectory",
			startDir: subDir,
			want:     devxDir,
		},
		{
			name:     "no_devx_directory",
			startDir: tmpDir,
			want:     "",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to test directory
			if err := os.Chdir(tt.startDir); err != nil {
				t.Fatalf("failed to change directory: %v", err)
			}
			
			got := FindProjectConfigDir()
			// Resolve symlinks for comparison (macOS /var vs /private/var issue)
			if got != "" {
				got, _ = filepath.EvalSymlinks(got)
			}
			want := tt.want
			if want != "" {
				want, _ = filepath.EvalSymlinks(want)
			}
			if got != want {
				t.Errorf("FindProjectConfigDir() = %v, want %v", got, want)
			}
		})
	}
}

func TestFindProjectConfigDirFromPath(t *testing.T) {
	// Create temp directory structure
	tmpDir, err := os.MkdirTemp("", "devx-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Create nested structure with .devx at different levels
	topLevel := filepath.Join(tmpDir, "top")
	topDevx := filepath.Join(topLevel, ".devx")
	midLevel := filepath.Join(topLevel, "middle")
	midDevx := filepath.Join(midLevel, ".devx")
	bottomLevel := filepath.Join(midLevel, "bottom")
	
	// Create all directories
	for _, dir := range []string{topDevx, midDevx, bottomLevel} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
	}
	
	tests := []struct {
		name      string
		startPath string
		want      string
	}{
		{
			name:      "find_nearest_devx",
			startPath: bottomLevel,
			want:      midDevx,
		},
		{
			name:      "find_in_current_dir",
			startPath: midLevel,
			want:      midDevx,
		},
		{
			name:      "find_in_parent",
			startPath: filepath.Join(topLevel, "other"),
			want:      topDevx,
		},
		{
			name:      "no_devx_found",
			startPath: tmpDir,
			want:      "",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findProjectConfigDirFromPath(tt.startPath)
			if got != tt.want {
				t.Errorf("findProjectConfigDirFromPath(%s) = %v, want %v", tt.startPath, got, tt.want)
			}
		})
	}
}

func TestGetConfigPath(t *testing.T) {
	// Create temp directory structure
	tmpDir, err := os.MkdirTemp("", "devx-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Save and change working directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer os.Chdir(oldWd)
	
	// Test with project config
	projectDir := filepath.Join(tmpDir, "project")
	devxDir := filepath.Join(projectDir, ".devx")
	projectConfig := filepath.Join(devxDir, "config.yaml")
	
	if err := os.MkdirAll(devxDir, 0755); err != nil {
		t.Fatalf("failed to create .devx dir: %v", err)
	}
	if err := os.WriteFile(projectConfig, []byte("test: true"), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	
	// Change to project directory
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	
	// Should return project config path
	got := GetConfigPath()
	// Resolve symlinks for comparison
	got, _ = filepath.EvalSymlinks(got)
	projectConfig, _ = filepath.EvalSymlinks(projectConfig)
	if got != projectConfig {
		t.Errorf("GetConfigPath() with project config = %v, want %v", got, projectConfig)
	}
	
	// Test without project config
	if err := os.Remove(projectConfig); err != nil {
		t.Fatalf("failed to remove config file: %v", err)
	}
	
	// Should return global config path
	got = GetConfigPath()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "devx", "config.yaml")
	if got != want {
		t.Errorf("GetConfigPath() without project config = %v, want %v", got, want)
	}
}

func TestGetSessionsPath(t *testing.T) {
	got := GetSessionsPath()
	
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home dir: %v", err)
	}
	
	want := filepath.Join(home, ".config", "devx", "sessions.json")
	if got != want {
		t.Errorf("GetSessionsPath() = %v, want %v", got, want)
	}
}

func TestGetTmuxTemplatePath(t *testing.T) {
	// Create temp directory structure
	tmpDir, err := os.MkdirTemp("", "devx-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Save and change working directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer os.Chdir(oldWd)
	
	// Create project with template
	projectDir := filepath.Join(tmpDir, "project")
	devxDir := filepath.Join(projectDir, ".devx")
	projectTemplate := filepath.Join(devxDir, "session.yaml.tmpl")
	
	if err := os.MkdirAll(devxDir, 0755); err != nil {
		t.Fatalf("failed to create .devx dir: %v", err)
	}
	if err := os.WriteFile(projectTemplate, []byte("template: project"), 0644); err != nil {
		t.Fatalf("failed to write template file: %v", err)
	}
	
	// Change to project directory
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	
	// Should return project template path
	got := GetTmuxTemplatePath()
	// Resolve symlinks for comparison
	got, _ = filepath.EvalSymlinks(got)
	projectTemplate, _ = filepath.EvalSymlinks(projectTemplate)
	if got != projectTemplate {
		t.Errorf("GetTmuxTemplatePath() with project template = %v, want %v", got, projectTemplate)
	}
	
	// Remove project template
	if err := os.Remove(projectTemplate); err != nil {
		t.Fatalf("failed to remove template file: %v", err)
	}
	
	// Should return global template path
	got = GetTmuxTemplatePath()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "devx", "session.yaml.tmpl")
	if got != want {
		t.Errorf("GetTmuxTemplatePath() without project template = %v, want %v", got, want)
	}
}

func TestGetConfigDir(t *testing.T) {
	// Create temp directory structure
	tmpDir, err := os.MkdirTemp("", "devx-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Save and change working directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer os.Chdir(oldWd)
	
	// Test with project config
	projectDir := filepath.Join(tmpDir, "project")
	devxDir := filepath.Join(projectDir, ".devx")
	
	if err := os.MkdirAll(devxDir, 0755); err != nil {
		t.Fatalf("failed to create .devx dir: %v", err)
	}
	
	// Change to project directory
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	
	// Should return project config dir
	got := GetConfigDir()
	// Resolve symlinks for comparison
	got, _ = filepath.EvalSymlinks(got)
	devxDir, _ = filepath.EvalSymlinks(devxDir)
	if got != devxDir {
		t.Errorf("GetConfigDir() with project = %v, want %v", got, devxDir)
	}
	
	// Test without project config
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change directory: %v", err)
	}
	
	// Should return global config dir
	got = GetConfigDir()
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "devx")
	if got != want {
		t.Errorf("GetConfigDir() without project = %v, want %v", got, want)
	}
}

func TestDiscoveryEdgeCases(t *testing.T) {
	// Test behavior at filesystem root
	t.Run("filesystem_root", func(t *testing.T) {
		// This should not find anything and return empty string
		got := findProjectConfigDirFromPath("/")
		if got != "" {
			t.Errorf("findProjectConfigDirFromPath('/') = %v, want empty", got)
		}
	})
	
	// Test with symlinks
	t.Run("with_symlinks", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "devx-symlink-test-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)
		
		// Create real directory with .devx
		realDir := filepath.Join(tmpDir, "real")
		devxDir := filepath.Join(realDir, ".devx")
		if err := os.MkdirAll(devxDir, 0755); err != nil {
			t.Fatalf("failed to create .devx dir: %v", err)
		}
		
		// Create symlink to real directory
		linkDir := filepath.Join(tmpDir, "link")
		if err := os.Symlink(realDir, linkDir); err != nil {
			t.Skipf("failed to create symlink (may require permissions): %v", err)
		}
		
		// Should find .devx through symlink - the path might be via the symlink
		got := findProjectConfigDirFromPath(linkDir)
		// The function might return the path via the symlink or the real path
		// Both are acceptable as long as it finds the .devx directory
		if got == "" {
			t.Error("findProjectConfigDirFromPath(symlink) should find .devx directory")
		}
		// Verify the directory actually exists
		if _, err := os.Stat(got); err != nil {
			t.Errorf("findProjectConfigDirFromPath(symlink) returned non-existent path: %v", got)
		}
	})
}