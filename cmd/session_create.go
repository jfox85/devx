package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	fePortFlag  int
	apiPortFlag int
	noTmuxFlag  bool
	noEditorFlag bool
	projectFlag string
)

var sessionCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new development session",
	Long:  `Create a new development session with a Git worktree.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionCreate,
}

func init() {
	sessionCmd.AddCommand(sessionCreateCmd)
	sessionCreateCmd.Flags().BoolVar(&detachFlag, "detach", false, "Detach existing worktree if it exists")
	sessionCreateCmd.Flags().IntVar(&fePortFlag, "fe-port", 0, "Frontend port (auto-allocated if not specified)")
	sessionCreateCmd.Flags().IntVar(&apiPortFlag, "api-port", 0, "API port (auto-allocated if not specified)")
	sessionCreateCmd.Flags().BoolVar(&noTmuxFlag, "no-tmux", false, "Skip launching tmux session")
	sessionCreateCmd.Flags().BoolVar(&noEditorFlag, "no-editor", false, "Skip launching editor")
	sessionCreateCmd.Flags().StringVarP(&projectFlag, "project", "p", "", "Project alias (defaults to current directory's project)")
}

func runSessionCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	
	// Load project registry
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return fmt.Errorf("failed to load project registry: %w", err)
	}
	
	// Determine project to use
	var project *config.Project
	var projectAlias string
	var projectPath string
	
	if projectFlag != "" {
		// Use specified project
		project, err = registry.GetProject(projectFlag)
		if err != nil {
			return fmt.Errorf("project '%s' not found", projectFlag)
		}
		projectAlias = projectFlag
		projectPath = project.Path
	} else {
		// Try to find project for current directory
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		
		// Check if current directory is within a registered project
		for alias, proj := range registry.Projects {
			if strings.HasPrefix(cwd, proj.Path) {
				project = proj
				projectAlias = alias
				projectPath = proj.Path
				break
			}
		}
		
		// If no project found, use current directory as a standalone project
		if project == nil {
			projectPath = cwd
			// Verify we're in a git repository
			if !isGitRepo(cwd) {
				return fmt.Errorf("not in a git repository. Use 'devx project add' to register this project first")
			}
		}
	}
	
	// Load existing sessions
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	
	// Check if session already exists in metadata
	if _, exists := store.GetSession(name); exists && !detachFlag {
		return fmt.Errorf("session '%s' already exists", name)
	}
	
	// Load configuration to get port names
	// Try project-specific config first
	cfg, err := config.GetProjectConfig(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}
	
	// Fall back to global config if no project config
	if cfg == nil {
		cfg, err = config.LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	}
	
	// Allocate or validate ports
	var portAllocation *session.PortAllocation
	
	if fePortFlag != 0 || apiPortFlag != 0 {
		// Legacy flag support - validate provided ports
		if fePortFlag == 0 || apiPortFlag == 0 {
			return fmt.Errorf("both --fe-port and --api-port must be specified together")
		}
		if err := session.ValidatePort(fePortFlag); err != nil {
			return fmt.Errorf("invalid frontend port: %w", err)
		}
		if err := session.ValidatePort(apiPortFlag); err != nil {
			return fmt.Errorf("invalid API port: %w", err)
		}
		if fePortFlag == apiPortFlag {
			return fmt.Errorf("frontend and API ports must be different")
		}
		
		// Create port allocation with legacy values mapped to service names
		portAllocation = &session.PortAllocation{
			Ports: map[string]int{
				"ui":  fePortFlag,
				"api": apiPortFlag,
			},
		}
	} else {
		// Auto-allocate ports based on config
		portAllocation, err = session.AllocatePorts(cfg.Ports)
		if err != nil {
			return fmt.Errorf("failed to allocate ports: %w", err)
		}
	}
	
	// Create the worktree
	if err := session.CreateWorktree(projectPath, name, detachFlag); err != nil {
		return err
	}
	
	// Copy bootstrap files from project root to worktree
	worktreePath := filepath.Join(projectPath, ".worktrees", name)
	if err := session.CopyBootstrapFiles(projectPath, worktreePath); err != nil {
		return fmt.Errorf("failed to copy bootstrap files: %w", err)
	}
	
	// Add session to metadata with project information
	if err := store.AddSessionWithProject(name, name, worktreePath, portAllocation.Ports, projectAlias, projectPath); err != nil {
		// If we fail to save metadata, we should clean up the worktree
		// For now, we'll just return the error
		return fmt.Errorf("failed to save session metadata: %w", err)
	}
	
	// Provision Caddy routes first to get hostnames
	routes, err := caddy.ProvisionSessionRoutesWithProject(name, portAllocation.Ports, projectAlias)
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
	}
	
	// Convert routes to hostnames for environment variables
	hostnames := make(map[string]string)
	if len(routes) > 0 {
		for serviceName := range routes {
			// Use the DNS-normalized service name for the hostname
			dnsServiceName := caddy.NormalizeDNSName(serviceName)
			if projectAlias != "" {
				hostnames[serviceName] = fmt.Sprintf("%s-%s-%s.localhost", projectAlias, name, dnsServiceName)
			} else {
				hostnames[serviceName] = fmt.Sprintf("%s-%s.localhost", name, dnsServiceName)
			}
		}
	}
	
	// Generate .envrc file
	envData := session.EnvrcData{
		Ports:  portAllocation.Ports,
		Routes: hostnames,
		Name:   name,
	}
	if err := session.GenerateEnvrc(worktreePath, envData); err != nil {
		return fmt.Errorf("failed to generate .envrc: %w", err)
	}
	
	// Generate tmuxp config
	tmuxpData := session.TmuxpData{
		Name:   name,
		Path:   worktreePath,
		Ports:  portAllocation.Ports,
		Routes: hostnames,
	}
	if err := session.GenerateTmuxpConfig(worktreePath, tmuxpData); err != nil {
		return fmt.Errorf("failed to generate tmuxp config: %w", err)
	}
	
	// Update session with route information
	if len(routes) > 0 {
		if err := store.UpdateSession(name, func(s *session.Session) {
			if s.Routes == nil {
				s.Routes = make(map[string]string)
			}
			for service, routeID := range routes {
				s.Routes[service] = routeID
			}
		}); err != nil {
			fmt.Printf("Warning: failed to save route information: %v\n", err)
		}
	}
	
	fmt.Printf("Created session '%s' at %s\n", name, worktreePath)
	if len(portAllocation.Ports) > 0 {
		fmt.Printf("Allocated ports:")
		for serviceName, port := range portAllocation.Ports {
			fmt.Printf(" %s_PORT=%d", strings.ToUpper(serviceName), port)
		}
		fmt.Printf("\n")
	}
	
	// Launch editor first (if configured and not disabled) so it's available immediately
	if !noEditorFlag {
		if err := session.LaunchEditorForSession(name, worktreePath); err != nil {
			fmt.Printf("Warning: Failed to launch editor: %v\n", err)
		}
	}
	
	// Launch tmux session unless disabled
	if !noTmuxFlag {
		if session.IsTmuxRunning() {
			fmt.Printf("Note: Already inside tmux. Session created but not launched.\n")
			fmt.Printf("To launch manually: tmuxp load %s/.tmuxp.yaml\n", worktreePath)
		} else {
			fmt.Printf("Launching tmux session...\n")
			if err := session.LaunchTmuxSession(worktreePath, name); err != nil {
				fmt.Printf("Warning: Failed to launch tmux session: %v\n", err)
				fmt.Printf("You can manually launch with: tmuxp load %s/.tmuxp.yaml\n", worktreePath)
			}
		}
	}
	
	return nil
}

func isGitRepo(path string) bool {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular() // .git can be a directory or a file (in worktrees)
}