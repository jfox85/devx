package deps

import (
	"os"
	"os/exec"
	"testing"

	"github.com/spf13/viper"
)

func TestGetDependencies(t *testing.T) {
	deps := GetDependencies()
	
	// Check we have the expected number of dependencies
	if len(deps) != 5 {
		t.Errorf("expected 5 dependencies, got %d", len(deps))
	}
	
	// Check required dependencies
	requiredCommands := map[string]bool{
		"git":   false,
		"tmux":  false,
		"tmuxp": false,
		"caddy": false,
	}
	
	optionalCommands := map[string]bool{
		"direnv": false,
	}
	
	for _, dep := range deps {
		if dep.Required {
			if _, exists := requiredCommands[dep.Command]; exists {
				requiredCommands[dep.Command] = true
			}
		} else {
			if _, exists := optionalCommands[dep.Command]; exists {
				optionalCommands[dep.Command] = true
			}
		}
		
		// Check all dependencies have descriptions and install hints
		if dep.Description == "" {
			t.Errorf("dependency %s missing description", dep.Name)
		}
		if dep.InstallHint == "" {
			t.Errorf("dependency %s missing install hint", dep.Name)
		}
	}
	
	// Verify all required commands were found
	for cmd, found := range requiredCommands {
		if !found {
			t.Errorf("required dependency %s not found in list", cmd)
		}
	}
	
	// Verify all optional commands were found
	for cmd, found := range optionalCommands {
		if !found {
			t.Errorf("optional dependency %s not found in list", cmd)
		}
	}
}

func TestCheckDependency(t *testing.T) {
	tests := []struct {
		name      string
		dep       Dependency
		wantAvail bool
	}{
		{
			name: "check_existing_command",
			dep: Dependency{
				Name:        "Go",
				Command:     "go",
				Required:    true,
				Description: "Go programming language",
				InstallHint: "Install from https://golang.org",
			},
			wantAvail: true, // Assuming go is available in test environment
		},
		{
			name: "check_nonexistent_command",
			dep: Dependency{
				Name:        "FakeCommand",
				Command:     "this-command-definitely-does-not-exist-12345",
				Required:    false,
				Description: "A fake command for testing",
				InstallHint: "This is just for testing",
			},
			wantAvail: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckDependency(tt.dep)
			
			if result.Available != tt.wantAvail {
				t.Errorf("CheckDependency() available = %v, want %v", result.Available, tt.wantAvail)
			}
			
			if result.Dependency.Name != tt.dep.Name {
				t.Errorf("CheckDependency() preserved name = %v, want %v", result.Dependency.Name, tt.dep.Name)
			}
			
			if tt.wantAvail && result.Version == "" {
				// Most commands should return some version info
				t.Logf("Warning: available command %s returned no version info", tt.dep.Command)
			}
			
			if !tt.wantAvail && result.Error == nil {
				t.Errorf("CheckDependency() for unavailable command should return error")
			}
		})
	}
}

func TestCheckAllDependencies(t *testing.T) {
	results := CheckAllDependencies()
	
	// Should return results for all dependencies
	deps := GetDependencies()
	if len(results) != len(deps) {
		t.Errorf("CheckAllDependencies() returned %d results, want %d", len(results), len(deps))
	}
	
	// Each result should correspond to a dependency
	for i, result := range results {
		if result.Dependency.Name != deps[i].Name {
			t.Errorf("Result %d has dependency %s, want %s", i, result.Dependency.Name, deps[i].Name)
		}
		
		// If command is available, it should be in PATH
		if result.Available {
			if _, err := exec.LookPath(result.Dependency.Command); err != nil {
				t.Errorf("Result says %s is available but not in PATH", result.Dependency.Command)
			}
		}
	}
}

func TestCheckConfiguredEditor(t *testing.T) {
	// Save original environment
	originalVisual := os.Getenv("VISUAL")
	originalEditor := os.Getenv("EDITOR")
	originalViperEditor := viper.GetString("editor")
	
	defer func() {
		os.Setenv("VISUAL", originalVisual)
		os.Setenv("EDITOR", originalEditor)
		viper.Set("editor", originalViperEditor)
	}()
	
	tests := []struct {
		name        string
		viperEditor string
		visual      string
		editor      string
		wantNil     bool
		wantCommand string
	}{
		{
			name:        "no_editor_configured",
			viperEditor: "",
			visual:      "",
			editor:      "",
			wantNil:     true,
		},
		{
			name:        "viper_editor_takes_precedence",
			viperEditor: "code",
			visual:      "vim",
			editor:      "nano",
			wantNil:     false,
			wantCommand: "code",
		},
		{
			name:        "visual_over_editor",
			viperEditor: "",
			visual:      "vim",
			editor:      "nano",
			wantNil:     false,
			wantCommand: "vim",
		},
		{
			name:        "editor_as_fallback",
			viperEditor: "",
			visual:      "",
			editor:      "nano",
			wantNil:     false,
			wantCommand: "nano",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			viper.Set("editor", tt.viperEditor)
			os.Setenv("VISUAL", tt.visual)
			os.Setenv("EDITOR", tt.editor)
			
			result := CheckConfiguredEditor()
			
			if tt.wantNil {
				if result != nil {
					t.Errorf("CheckConfiguredEditor() = %v, want nil", result)
				}
			} else {
				if result == nil {
					t.Errorf("CheckConfiguredEditor() = nil, want non-nil")
				} else if result.Dependency.Command != tt.wantCommand {
					t.Errorf("CheckConfiguredEditor() command = %v, want %v", result.Dependency.Command, tt.wantCommand)
				}
			}
		})
	}
}

func TestGetEditorCommand(t *testing.T) {
	// Save original environment
	originalVisual := os.Getenv("VISUAL")
	originalEditor := os.Getenv("EDITOR")
	originalViperEditor := viper.GetString("editor")
	
	defer func() {
		os.Setenv("VISUAL", originalVisual)
		os.Setenv("EDITOR", originalEditor)
		viper.Set("editor", originalViperEditor)
	}()
	
	tests := []struct {
		name        string
		viperEditor string
		visual      string
		editor      string
		want        string
	}{
		{
			name:        "viper_config_precedence",
			viperEditor: "code",
			visual:      "vim",
			editor:      "nano",
			want:        "code",
		},
		{
			name:        "visual_env_var",
			viperEditor: "",
			visual:      "vim",
			editor:      "nano",
			want:        "vim",
		},
		{
			name:        "editor_env_var",
			viperEditor: "",
			visual:      "",
			editor:      "nano",
			want:        "nano",
		},
		{
			name:        "no_editor",
			viperEditor: "",
			visual:      "",
			editor:      "",
			want:        "",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Set("editor", tt.viperEditor)
			os.Setenv("VISUAL", tt.visual)
			os.Setenv("EDITOR", tt.editor)
			
			got := getEditorCommand()
			if got != tt.want {
				t.Errorf("getEditorCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrintResults(t *testing.T) {
	// This is mainly a smoke test to ensure PrintResults doesn't panic
	// We can't easily test the output without capturing stdout
	
	results := []CheckResult{
		{
			Dependency: Dependency{
				Name:        "Git",
				Command:     "git",
				Required:    true,
				Description: "Version control",
				InstallHint: "brew install git",
			},
			Available: true,
			Version:   "git version 2.30.0",
		},
		{
			Dependency: Dependency{
				Name:        "Missing",
				Command:     "missing",
				Required:    true,
				Description: "Missing dependency",
				InstallHint: "install missing",
			},
			Available: false,
		},
		{
			Dependency: Dependency{
				Name:        "Optional",
				Command:     "optional",
				Required:    false,
				Description: "Optional dependency",
				InstallHint: "install optional",
			},
			Available: false,
		},
	}
	
	editorResult := &CheckResult{
		Dependency: Dependency{
			Name:        "Editor",
			Command:     "vim",
			Required:    false,
			Description: "Configured editor: vim",
			InstallHint: "install vim",
		},
		Available: true,
		Version:   "VIM 8.2",
	}
	
	// This should not panic
	PrintResults(results, editorResult)
	
	// Test with nil editor result
	PrintResults(results, nil)
	
	// Test with empty results
	PrintResults([]CheckResult{}, nil)
}

func TestDependencyStructure(t *testing.T) {
	// Test that Dependency struct has all expected fields
	dep := Dependency{
		Name:        "Test",
		Command:     "test",
		Required:    true,
		Description: "Test description",
		InstallHint: "Test install hint",
	}
	
	if dep.Name != "Test" {
		t.Errorf("Dependency.Name = %v, want Test", dep.Name)
	}
	if dep.Command != "test" {
		t.Errorf("Dependency.Command = %v, want test", dep.Command)
	}
	if !dep.Required {
		t.Errorf("Dependency.Required = %v, want true", dep.Required)
	}
	if dep.Description != "Test description" {
		t.Errorf("Dependency.Description = %v, want Test description", dep.Description)
	}
	if dep.InstallHint != "Test install hint" {
		t.Errorf("Dependency.InstallHint = %v, want Test install hint", dep.InstallHint)
	}
}

func TestCheckResultStructure(t *testing.T) {
	// Test that CheckResult struct has all expected fields
	dep := Dependency{Name: "Test", Command: "test"}
	result := CheckResult{
		Dependency: dep,
		Available:  true,
		Version:    "1.0.0",
		Error:      nil,
	}
	
	if result.Dependency.Name != "Test" {
		t.Errorf("CheckResult.Dependency.Name = %v, want Test", result.Dependency.Name)
	}
	if !result.Available {
		t.Errorf("CheckResult.Available = %v, want true", result.Available)
	}
	if result.Version != "1.0.0" {
		t.Errorf("CheckResult.Version = %v, want 1.0.0", result.Version)
	}
	if result.Error != nil {
		t.Errorf("CheckResult.Error = %v, want nil", result.Error)
	}
}

func TestVersionExtraction(t *testing.T) {
	// Test version extraction with different command patterns
	// Using 'ls' which should be available on all Unix systems
	dep := Dependency{
		Name:        "List",
		Command:     "ls",
		Required:    true,
		Description: "List command",
		InstallHint: "Should be pre-installed",
	}
	
	result := CheckDependency(dep)
	
	if !result.Available {
		t.Skip("ls command not available, skipping version extraction test")
	}
	
	// ls might not support --version on all systems, so we just check it was attempted
	// The important thing is that CheckDependency doesn't crash when version check fails
	if result.Error != nil {
		t.Errorf("CheckDependency returned error for available command: %v", result.Error)
	}
}