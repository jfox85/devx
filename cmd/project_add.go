package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jfox85/devx/config"
	"github.com/spf13/cobra"
)

var (
	projectName        string
	projectDesc        string
	projectDefaultBranch string
)

var projectAddCmd = &cobra.Command{
	Use:   "add <path> --alias <alias>",
	Short: "Add a new project to devx",
	Long: `Add a new project to devx, allowing you to create sessions for it.
	
Examples:
  devx project add . --alias myapp
  devx project add ~/code/backend --alias backend --name "Backend API"
  devx project add /path/to/frontend --alias fe --desc "React frontend" --default-branch develop`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		projectPath := args[0]
		
		// Get alias from flag
		alias, err := cmd.Flags().GetString("alias")
		if err != nil || alias == "" {
			return fmt.Errorf("--alias flag is required")
		}
		
		// Validate project path exists
		absPath, err := filepath.Abs(projectPath)
		if err != nil {
			return fmt.Errorf("failed to resolve project path: %w", err)
		}
		
		// Check if path is a directory
		info, err := os.Stat(absPath)
		if err != nil {
			return fmt.Errorf("project path does not exist: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("project path must be a directory")
		}
		
		// Check if it's a git repository
		gitPath := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitPath); err != nil {
			fmt.Printf("Warning: %s does not appear to be a git repository\n", absPath)
		}
		
		// Load project registry
		registry, err := config.LoadProjectRegistry()
		if err != nil {
			return fmt.Errorf("failed to load project registry: %w", err)
		}
		
		// Check if alias already exists
		if _, exists := registry.Projects[alias]; exists {
			return fmt.Errorf("project alias '%s' already exists", alias)
		}
		
		// Check if path is already registered
		if existingAlias, _, err := registry.FindProjectByPath(absPath); err == nil && existingAlias != "" {
			return fmt.Errorf("project path already registered with alias '%s'", existingAlias)
		}
		
		// Use alias as name if not provided
		if projectName == "" {
			projectName = alias
		}
		
		// Create project
		project := &config.Project{
			Name:          projectName,
			Path:          absPath,
			Description:   projectDesc,
			DefaultBranch: projectDefaultBranch,
		}
		
		// Add to registry
		if err := registry.AddProject(alias, project); err != nil {
			return fmt.Errorf("failed to add project: %w", err)
		}
		
		fmt.Printf("Successfully added project '%s' (%s)\n", alias, projectName)
		fmt.Printf("Path: %s\n", absPath)
		
		// Check if project has .devx config
		devxPath := config.GetProjectConfigDir(absPath)
		if _, err := os.Stat(devxPath); err == nil {
			fmt.Printf("Found .devx configuration in project\n")
		} else {
			fmt.Printf("\nTip: Create a .devx/config.yaml in your project to define custom services and settings\n")
		}
		
		return nil
	},
}

func init() {
	projectCmd.AddCommand(projectAddCmd)
	
	projectAddCmd.Flags().StringP("alias", "a", "", "Short alias for the project (required)")
	projectAddCmd.Flags().StringVarP(&projectName, "name", "n", "", "Display name for the project")
	projectAddCmd.Flags().StringVarP(&projectDesc, "desc", "d", "", "Description of the project")
	projectAddCmd.Flags().StringVar(&projectDefaultBranch, "default-branch", "main", "Default branch for new sessions")
	
	projectAddCmd.MarkFlagRequired("alias")
}