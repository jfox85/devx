package deps

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jfox85/devx/version"
	"github.com/spf13/viper"
)

type Dependency struct {
	Name        string
	Command     string
	Required    bool
	Description string
	InstallHint string
}

type CheckResult struct {
	Dependency Dependency
	Available  bool
	Version    string
	Error      error
}

// GetDependencies returns the list of dependencies to check
func GetDependencies() []Dependency {
	return []Dependency{
		{
			Name:        "Git",
			Command:     "git",
			Required:    true,
			Description: "Version control system for managing worktrees",
			InstallHint: "Install with: brew install git",
		},
		{
			Name:        "Tmux",
			Command:     "tmux",
			Required:    true,
			Description: "Terminal multiplexer for session management",
			InstallHint: "Install with: brew install tmux",
		},
		{
			Name:        "Tmuxp",
			Command:     "tmuxp",
			Required:    true,
			Description: "Tmux session manager",
			InstallHint: "Install with: pip install tmuxp",
		},
		{
			Name:        "Caddy",
			Command:     "caddy",
			Required:    true,
			Description: "Web server for local development routing",
			InstallHint: "Install with: brew install caddy",
		},
		{
			Name:        "Direnv",
			Command:     "direnv",
			Required:    false,
			Description: "Environment variable management (recommended)",
			InstallHint: "Install with: brew install direnv",
		},
	}
}

// CheckDependency checks if a single dependency is available
func CheckDependency(dep Dependency) CheckResult {
	result := CheckResult{
		Dependency: dep,
		Available:  false,
	}

	// Check if command exists
	_, err := exec.LookPath(dep.Command)
	if err != nil {
		result.Error = err
		return result
	}

	result.Available = true

	// Try to get version information
	versionCmd := exec.Command(dep.Command, "--version")
	output, err := versionCmd.Output()
	if err == nil {
		// Clean up version output (first line, trim whitespace)
		lines := strings.Split(string(output), "\n")
		if len(lines) > 0 {
			result.Version = strings.TrimSpace(lines[0])
		}
	}

	return result
}

// CheckAllDependencies checks all dependencies and returns results
func CheckAllDependencies() []CheckResult {
	deps := GetDependencies()
	results := make([]CheckResult, len(deps))

	for i, dep := range deps {
		results[i] = CheckDependency(dep)
	}

	return results
}

// CheckConfiguredEditor checks if the configured editor is available
func CheckConfiguredEditor() *CheckResult {
	editorCmd := getEditorCommand()
	if editorCmd == "" {
		return nil // No editor configured
	}

	dep := Dependency{
		Name:        "Editor",
		Command:     editorCmd,
		Required:    false,
		Description: fmt.Sprintf("Configured editor: %s", editorCmd),
		InstallHint: fmt.Sprintf("Check your editor configuration or install %s", editorCmd),
	}

	result := CheckDependency(dep)
	return &result
}

// getEditorCommand returns the configured editor command
func getEditorCommand() string {
	// Check devx config first
	if editor := viper.GetString("editor"); editor != "" {
		return editor
	}

	// Check environment variables
	if visual := os.Getenv("VISUAL"); visual != "" {
		return visual
	}

	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}

	return ""
}

// PrintResults prints dependency check results
func PrintResults(results []CheckResult, editorResult *CheckResult) {
	hasIssues := false
	missingRequired := []string{}
	missingOptional := []string{}

	fmt.Printf("Dependency Check (%s):\n", version.Get().String())
	fmt.Println("=================")

	for _, result := range results {
		status := "✓"
		if !result.Available {
			status = "✗"
			hasIssues = true
			if result.Dependency.Required {
				missingRequired = append(missingRequired, result.Dependency.Name)
			} else {
				missingOptional = append(missingOptional, result.Dependency.Name)
			}
		}

		fmt.Printf("%s %s", status, result.Dependency.Name)
		if result.Available && result.Version != "" {
			fmt.Printf(" (%s)", result.Version)
		}
		fmt.Printf(" - %s\n", result.Dependency.Description)

		if !result.Available {
			fmt.Printf("  └─ %s\n", result.Dependency.InstallHint)
		}
	}

	// Check configured editor
	if editorResult != nil {
		status := "✓"
		if !editorResult.Available {
			status = "✗"
			hasIssues = true
			missingOptional = append(missingOptional, "Editor")
		}

		fmt.Printf("%s %s", status, editorResult.Dependency.Name)
		if editorResult.Available && editorResult.Version != "" {
			fmt.Printf(" (%s)", editorResult.Version)
		}
		fmt.Printf(" - %s\n", editorResult.Dependency.Description)

		if !editorResult.Available {
			fmt.Printf("  └─ %s\n", editorResult.Dependency.InstallHint)
		}
	}

	fmt.Println()

	// Summary
	if len(missingRequired) > 0 {
		fmt.Printf("⚠️  Missing required dependencies: %s\n", strings.Join(missingRequired, ", "))
		fmt.Println("   Some features may not work properly.")
	}

	if len(missingOptional) > 0 {
		fmt.Printf("ℹ️  Missing optional dependencies: %s\n", strings.Join(missingOptional, ", "))
		fmt.Println("   These are recommended but not required.")
	}

	if !hasIssues {
		fmt.Println("✅ All dependencies are available!")
	}

	fmt.Println()
}
