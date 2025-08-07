package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectRegistry(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "devx-projects-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Override home directory for testing
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)
	
	t.Run("load_empty_registry", func(t *testing.T) {
		registry, err := LoadProjectRegistry()
		if err != nil {
			t.Fatalf("failed to load empty registry: %v", err)
		}
		
		if registry.Projects == nil {
			t.Error("Projects map should be initialized")
		}
		
		if len(registry.Projects) != 0 {
			t.Errorf("Empty registry should have 0 projects, got %d", len(registry.Projects))
		}
	})
	
	t.Run("add_project", func(t *testing.T) {
		registry, err := LoadProjectRegistry()
		if err != nil {
			t.Fatalf("failed to load registry: %v", err)
		}
		
		// Create a test project directory
		projectPath := filepath.Join(tmpDir, "testproject")
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			t.Fatalf("failed to create project dir: %v", err)
		}
		
		project := &Project{
			Name:          "Test Project",
			Path:          projectPath,
			Description:   "A test project",
			DefaultBranch: "main",
		}
		
		err = registry.AddProject("test", project)
		if err != nil {
			t.Fatalf("failed to add project: %v", err)
		}
		
		// Verify project was added
		if len(registry.Projects) != 1 {
			t.Errorf("Registry should have 1 project, got %d", len(registry.Projects))
		}
		
		// Verify path was made absolute
		absPath, _ := filepath.Abs(projectPath)
		if registry.Projects["test"].Path != absPath {
			t.Errorf("Project path should be absolute: got %s, want %s", 
				registry.Projects["test"].Path, absPath)
		}
	})
	
	t.Run("add_project_invalid_path", func(t *testing.T) {
		registry := &ProjectRegistry{Projects: make(map[string]*Project)}
		
		project := &Project{
			Name: "Invalid",
			Path: "/this/path/does/not/exist/at/all",
		}
		
		err := registry.AddProject("invalid", project)
		if err == nil {
			t.Error("Adding project with invalid path should fail")
		}
	})
	
	t.Run("get_project", func(t *testing.T) {
		registry := &ProjectRegistry{
			Projects: map[string]*Project{
				"test": {
					Name: "Test Project",
					Path: "/test/path",
				},
			},
		}
		
		project, err := registry.GetProject("test")
		if err != nil {
			t.Fatalf("failed to get existing project: %v", err)
		}
		
		if project.Name != "Test Project" {
			t.Errorf("Got wrong project: %s", project.Name)
		}
		
		// Test non-existent project
		_, err = registry.GetProject("nonexistent")
		if err == nil {
			t.Error("Getting non-existent project should fail")
		}
	})
	
	t.Run("remove_project", func(t *testing.T) {
		// Create registry with test data
		registry := &ProjectRegistry{
			Projects: map[string]*Project{
				"test1": {Name: "Test 1", Path: "/test1"},
				"test2": {Name: "Test 2", Path: "/test2"},
			},
		}
		
		// Remove a project
		err := registry.RemoveProject("test1")
		if err != nil {
			t.Fatalf("failed to remove project: %v", err)
		}
		
		if len(registry.Projects) != 1 {
			t.Errorf("Registry should have 1 project after removal, got %d", len(registry.Projects))
		}
		
		if _, exists := registry.Projects["test1"]; exists {
			t.Error("Removed project should not exist")
		}
		
		// Try to remove non-existent project
		err = registry.RemoveProject("nonexistent")
		if err == nil {
			t.Error("Removing non-existent project should fail")
		}
	})
	
	t.Run("find_project_by_path", func(t *testing.T) {
		projectPath := filepath.Join(tmpDir, "findtest")
		if err := os.MkdirAll(projectPath, 0755); err != nil {
			t.Fatalf("failed to create project dir: %v", err)
		}
		
		absPath, _ := filepath.Abs(projectPath)
		
		registry := &ProjectRegistry{
			Projects: map[string]*Project{
				"find": {
					Name: "Find Test",
					Path: absPath,
				},
			},
		}
		
		// Find by exact path
		alias, project, err := registry.FindProjectByPath(projectPath)
		if err != nil {
			t.Fatalf("failed to find project by path: %v", err)
		}
		
		if alias != "find" {
			t.Errorf("Wrong alias returned: got %s, want find", alias)
		}
		
		if project.Name != "Find Test" {
			t.Errorf("Wrong project returned: %s", project.Name)
		}
		
		// Find non-existent path
		alias, project, err = registry.FindProjectByPath("/nonexistent")
		if err != nil {
			t.Fatalf("FindProjectByPath returned error for non-existent: %v", err)
		}
		
		if alias != "" || project != nil {
			t.Error("FindProjectByPath should return empty for non-existent path")
		}
	})
}

func TestProjectRegistrySaveLoad(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "devx-save-load-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Create config directory structure
	configDir := filepath.Join(tmpDir, ".config", "devx")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	
	// Override home directory
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)
	
	// Create test registry
	registry := &ProjectRegistry{
		Projects: map[string]*Project{
			"proj1": {
				Name:          "Project One",
				Path:          "/path/to/proj1",
				Description:   "First project",
				DefaultBranch: "main",
			},
			"proj2": {
				Name:        "Project Two",
				Path:        "/path/to/proj2",
				Description: "Second project",
			},
		},
	}
	
	// Save registry
	if err := registry.Save(); err != nil {
		t.Fatalf("failed to save registry: %v", err)
	}
	
	// Verify file was created
	registryPath := GetProjectRegistryPath()
	if _, err := os.Stat(registryPath); err != nil {
		t.Fatalf("registry file not created: %v", err)
	}
	
	// Load registry
	loaded, err := LoadProjectRegistry()
	if err != nil {
		t.Fatalf("failed to load registry: %v", err)
	}
	
	// Compare loaded with original
	if !reflect.DeepEqual(registry.Projects, loaded.Projects) {
		t.Errorf("Loaded registry doesn't match original")
	}
	
	// Verify JSON format
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("failed to read registry file: %v", err)
	}
	
	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		t.Fatalf("registry file is not valid JSON: %v", err)
	}
	
	// Should have projects key
	if _, ok := jsonData["projects"]; !ok {
		t.Error("JSON should have 'projects' key")
	}
}

func TestGetProjectRegistryPath(t *testing.T) {
	// Save original HOME
	oldHome := os.Getenv("HOME")
	defer os.Setenv("HOME", oldHome)
	
	testHome := "/test/home"
	os.Setenv("HOME", testHome)
	
	got := GetProjectRegistryPath()
	want := filepath.Join(testHome, ".config", "devx", "projects.json")
	
	if got != want {
		t.Errorf("GetProjectRegistryPath() = %v, want %v", got, want)
	}
}

func TestGetProjectConfigDir(t *testing.T) {
	projectPath := "/test/project"
	got := GetProjectConfigDir(projectPath)
	want := filepath.Join(projectPath, ".devx")
	
	if got != want {
		t.Errorf("GetProjectConfigDir() = %v, want %v", got, want)
	}
}

func TestGetProjectConfig(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "devx-project-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Create project with config
	projectPath := filepath.Join(tmpDir, "project")
	configDir := filepath.Join(projectPath, ".devx")
	configFile := filepath.Join(configDir, "config.yaml")
	
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	
	// Write test config
	configContent := `
basedomain: test.local
caddy_api: http://localhost:3000
editor: code
`
	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	
	// Load project config
	cfg, err := GetProjectConfig(projectPath)
	if err != nil {
		t.Fatalf("failed to get project config: %v", err)
	}
	
	if cfg == nil {
		t.Fatal("GetProjectConfig returned nil for existing config")
	}
	
	if cfg.BaseDomain != "test.local" {
		t.Errorf("Wrong basedomain: got %s, want test.local", cfg.BaseDomain)
	}
	
	if cfg.CaddyAPI != "http://localhost:3000" {
		t.Errorf("Wrong caddy_api: got %s, want http://localhost:3000", cfg.CaddyAPI)
	}
	
	// Test non-existent config
	cfg, err = GetProjectConfig(tmpDir)
	if err != nil {
		t.Fatalf("GetProjectConfig failed for non-existent: %v", err)
	}
	
	if cfg != nil {
		t.Error("GetProjectConfig should return nil for non-existent config")
	}
}

func TestProjectStruct(t *testing.T) {
	// Test JSON marshaling/unmarshaling
	project := Project{
		Name:          "Test",
		Path:          "/test/path",
		Description:   "Test description",
		DefaultBranch: "develop",
	}
	
	data, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("failed to marshal project: %v", err)
	}
	
	var decoded Project
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal project: %v", err)
	}
	
	if !reflect.DeepEqual(project, decoded) {
		t.Error("Project doesn't survive JSON roundtrip")
	}
	
	// Test omitempty for optional fields
	minProject := Project{
		Name: "Minimal",
		Path: "/min/path",
	}
	
	data, err = json.Marshal(minProject)
	if err != nil {
		t.Fatalf("failed to marshal minimal project: %v", err)
	}
	
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("failed to unmarshal to map: %v", err)
	}
	
	// Optional fields should not be present
	if _, hasDesc := jsonMap["description"]; hasDesc {
		t.Error("Empty description should be omitted from JSON")
	}
	
	if _, hasBranch := jsonMap["default_branch"]; hasBranch {
		t.Error("Empty default_branch should be omitted from JSON")
	}
}

func TestProjectRegistryNilHandling(t *testing.T) {
	// Test that methods handle nil Projects map correctly
	registry := &ProjectRegistry{}
	
	// AddProject should initialize the map
	tmpDir, err := os.MkdirTemp("", "devx-nil-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Create config directory structure
	configDir := filepath.Join(tmpDir, ".config", "devx")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	
	// Override HOME to use temp dir
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)
	
	// Create a project directory that exists
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	
	project := &Project{
		Name: "Test",
		Path: projectDir,
	}
	
	err = registry.AddProject("test", project)
	if err != nil {
		t.Fatalf("failed to add project to nil map: %v", err)
	}
	
	if registry.Projects == nil {
		t.Error("AddProject should initialize nil Projects map")
	}
	
	if len(registry.Projects) != 1 {
		t.Errorf("Registry should have 1 project, got %d", len(registry.Projects))
	}
}