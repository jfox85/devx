package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type EnvrcData struct {
	Ports  map[string]int    // service name -> port number
	Routes map[string]string // service name -> hostname
	Name   string
}

// GenerateEnvrc creates an .envrc file in the worktree directory
func GenerateEnvrc(worktreePath string, data EnvrcData) error {
	// Generate the .envrc content dynamically
	var lines []string

	// Add ports in sorted order for consistent output
	var serviceNames []string
	for name := range data.Ports {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	for _, serviceName := range serviceNames {
		port := data.Ports[serviceName]
		// Convert service name to PORT environment variable (e.g., "ui" -> "UI_PORT")
		portVar := strings.ToUpper(serviceName) + "_PORT"
		lines = append(lines, fmt.Sprintf("export %s=%d", portVar, port))
	}

	// Add hostname environment variables if routes exist
	if len(data.Routes) > 0 {
		lines = append(lines, "")
		lines = append(lines, "# HTTP hostnames")

		// Sort route names for consistent output
		var routeNames []string
		for name := range data.Routes {
			routeNames = append(routeNames, name)
		}
		sort.Strings(routeNames)

		for _, serviceName := range routeNames {
			hostname := data.Routes[serviceName]
			// Convert service name to HOST environment variable
			// e.g., "ui" -> "UI_HOST", "auth-service" -> "AUTH_SERVICE_HOST"
			hostVar := strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_")) + "_HOST"
			lines = append(lines, fmt.Sprintf("export %s=http://%s", hostVar, hostname))
		}
	}

	// Add session name
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("export SESSION_NAME=%s", data.Name))

	content := strings.Join(lines, "\n") + "\n"

	envrcPath := filepath.Join(worktreePath, ".envrc")
	if err := os.WriteFile(envrcPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write .envrc file: %w", err)
	}

	// Run direnv allow if direnv is available
	if err := runDirenvAllow(worktreePath); err != nil {
		// Don't fail if direnv is not available, just log it
		fmt.Printf("Note: direnv not available or failed to allow: %v\n", err)
	}

	return nil
}

// runDirenvAllow runs 'direnv allow' in the specified directory
func runDirenvAllow(path string) error {
	// Check if direnv is available
	if _, err := exec.LookPath("direnv"); err != nil {
		return fmt.Errorf("direnv not found in PATH")
	}

	cmd := exec.Command("direnv", "allow")
	cmd.Dir = path

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("direnv allow failed: %w\n%s", err, output)
	}

	return nil
}
