package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Project represents a registered project in devx
type Project struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Description   string `json:"description,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// ProjectRegistry manages multiple projects
type ProjectRegistry struct {
	Projects map[string]*Project `json:"projects"`
}

// LoadProjectRegistry loads the project registry from the projects.json file
func LoadProjectRegistry() (*ProjectRegistry, error) {
	registryPath := GetProjectRegistryPath()

	// Create config directory if it doesn't exist
	dir := filepath.Dir(registryPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// If file doesn't exist, return empty registry
	if _, err := os.Stat(registryPath); os.IsNotExist(err) {
		return &ProjectRegistry{Projects: make(map[string]*Project)}, nil
	}

	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read projects file: %w", err)
	}

	var registry ProjectRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("failed to parse projects file: %w", err)
	}

	if registry.Projects == nil {
		registry.Projects = make(map[string]*Project)
	}

	return &registry, nil
}

// Save saves the project registry to disk
func (r *ProjectRegistry) Save() error {
	registryPath := GetProjectRegistryPath()

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal projects: %w", err)
	}

	if err := os.WriteFile(registryPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write projects file: %w", err)
	}

	return nil
}

// AddProject adds a new project to the registry
func (r *ProjectRegistry) AddProject(alias string, project *Project) error {
	if r.Projects == nil {
		r.Projects = make(map[string]*Project)
	}

	// Validate project path exists
	if _, err := os.Stat(project.Path); err != nil {
		return fmt.Errorf("project path does not exist: %w", err)
	}

	// Make path absolute
	absPath, err := filepath.Abs(project.Path)
	if err != nil {
		return fmt.Errorf("failed to resolve project path: %w", err)
	}
	project.Path = absPath

	r.Projects[alias] = project
	return r.Save()
}

// RemoveProject removes a project from the registry
func (r *ProjectRegistry) RemoveProject(alias string) error {
	if _, exists := r.Projects[alias]; !exists {
		return fmt.Errorf("project '%s' not found", alias)
	}

	delete(r.Projects, alias)
	return r.Save()
}

// GetProject retrieves a project by alias
func (r *ProjectRegistry) GetProject(alias string) (*Project, error) {
	project, exists := r.Projects[alias]
	if !exists {
		return nil, fmt.Errorf("project '%s' not found", alias)
	}
	return project, nil
}

// GetProjectRegistryPath returns the path to the projects.json file
func GetProjectRegistryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".config", "devx", "projects.json")
}

// FindProjectByPath searches for a project by its path
func (r *ProjectRegistry) FindProjectByPath(path string) (string, *Project, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}

	for alias, project := range r.Projects {
		if project.Path == absPath {
			return alias, project, nil
		}
	}

	return "", nil, nil
}

// GetProjectConfigDir returns the .devx directory path for a project
func GetProjectConfigDir(projectPath string) string {
	return filepath.Join(projectPath, ".devx")
}

// GetProjectConfig loads the config for a specific project
func GetProjectConfig(projectPath string) (*Config, error) {
	configPath := filepath.Join(GetProjectConfigDir(projectPath), "config.yaml")

	// Check if project config exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Return nil to indicate no project-specific config
		return nil, nil
	}

	// Load the project-specific config
	v := viper.New()
	v.SetConfigFile(configPath)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal project config: %w", err)
	}

	return &cfg, nil
}
