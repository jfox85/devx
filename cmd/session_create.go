package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	fePortFlag            int
	apiPortFlag           int
	noTmuxFlag            bool
	projectFlag           string
	reuseFlag             bool
	createColorFlag       string
	createDisplayNameFlag string
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
	sessionCreateCmd.Flags().BoolVar(&reuseFlag, "reuse", false, "Reuse existing worktree if it exists at the expected path")
	sessionCreateCmd.Flags().IntVar(&fePortFlag, "fe-port", 0, "Frontend port (auto-allocated if not specified)")
	sessionCreateCmd.Flags().IntVar(&apiPortFlag, "api-port", 0, "API port (auto-allocated if not specified)")
	sessionCreateCmd.Flags().BoolVar(&noTmuxFlag, "no-tmux", false, "Skip launching tmux session")
	sessionCreateCmd.Flags().StringVarP(&projectFlag, "project", "p", "", "Project alias (defaults to current directory's project)")
	sessionCreateCmd.Flags().StringVar(&createColorFlag, "color", "", "Session color (auto-assigned if not specified)")
	sessionCreateCmd.Flags().StringVar(&createDisplayNameFlag, "display-name", "", "Display name for the session")
}

func runSessionCreate(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Validate session name to prevent shell injection, argument injection, and
	// path traversal. Names are used as git branch names, tmux targets, and
	// path components under .worktrees/.
	if !session.IsValidSessionName(name) {
		return fmt.Errorf("invalid session name %q: must start with a letter or digit, contain only letters/digits/dots/underscores/hyphens/slashes, and must not contain '..' or empty path segments", name)
	}

	// Validate --color and --display-name flags early (before side effects)
	if createColorFlag != "" && !session.IsValidColor(createColorFlag) {
		return fmt.Errorf("invalid color %q. Valid colors: %s", createColorFlag, strings.Join(session.Palette, ", "))
	}
	if createDisplayNameFlag != "" && !session.IsValidDisplayName(createDisplayNameFlag) {
		return fmt.Errorf("display name too long (max %d characters)", session.MaxDisplayNameLen)
	}

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
	existingSession, sessionExists := store.GetSession(name)
	if sessionExists && !detachFlag && !reuseFlag {
		return fmt.Errorf("session '%s' already exists in metadata. Use --reuse to reuse it or --detach to recreate", name)
	}

	// If reuse flag is set and session exists, verify the worktree is still valid
	if reuseFlag && sessionExists {
		// Check if the worktree still exists
		worktreeExists, err := session.WorktreeExists(projectPath, existingSession.Path)
		if err != nil {
			return fmt.Errorf("failed to check worktree existence: %w", err)
		}

		if worktreeExists {
			fmt.Printf("Reusing existing session '%s' at %s\n", name, existingSession.Path)
			// Skip to tmux launch if not disabled
			if !noTmuxFlag {
				if session.IsTmuxRunning() {
					fmt.Printf("Note: Already inside tmux. Session exists but not launched.\n")
					fmt.Printf("To launch manually: tmuxp load %s/.tmuxp.yaml\n", existingSession.Path)
				} else {
					fmt.Printf("Launching tmux session...\n")
					if err := session.LaunchTmuxSession(existingSession.Path, name); err != nil {
						fmt.Printf("Warning: Failed to launch tmux session: %v\n", err)
						fmt.Printf("You can manually launch with: tmuxp load %s/.tmuxp.yaml\n", existingSession.Path)
					}
				}
			}
			return nil
		}
		// Worktree doesn't exist, continue with creation
		fmt.Printf("Session metadata exists but worktree is missing, recreating...\n")
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

	// Check if auto-pull is enabled for this project
	if project != nil && project.AutoPullOnCreate {
		fmt.Printf("Pulling latest changes from origin/%s...\n", project.DefaultBranch)
		if err := session.PullFromOrigin(projectPath, project.DefaultBranch); err != nil {
			// Only show warning for actual errors, not network issues
			if strings.Contains(err.Error(), "uncommitted changes") {
				return fmt.Errorf("cannot create session: %w", err)
			}
			// For other errors, just warn and continue
			fmt.Printf("Warning: Could not pull from origin: %v\n", err)
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

	// Create the worktree (or adopt an existing one if the branch is already checked out)
	worktreePath, err := session.CreateWorktree(projectPath, name, detachFlag)
	if err != nil {
		return err
	}
	if err := session.CopyBootstrapFiles(projectPath, worktreePath); err != nil {
		return fmt.Errorf("failed to copy bootstrap files: %w", err)
	}

	// Add session to metadata with project information
	// Get the branch name for the session
	branchName := name // Default to session name
	gitCmd := exec.Command("git", "branch", "--show-current")
	gitCmd.Dir = worktreePath
	if output, err := gitCmd.Output(); err == nil {
		branchName = strings.TrimSpace(string(output))
	}

	if err := store.AddSessionWithProject(name, branchName, worktreePath, portAllocation.Ports, projectAlias, projectPath); err != nil {
		// If we fail to save metadata, we should clean up the worktree
		// For now, we'll just return the error
		return fmt.Errorf("failed to save session metadata: %w", err)
	}

	// Set color (auto-assign if not specified) and display name
	color := createColorFlag
	if color == "" {
		color = session.AutoColor(name)
	}
	if err := store.UpdateSession(name, func(s *session.Session) {
		s.Color = color
		if createDisplayNameFlag != "" {
			s.DisplayName = createDisplayNameFlag
		}
	}); err != nil {
		fmt.Printf("Warning: failed to set color/display-name: %v\n", err)
	}

	// Build hostname map for environment variables
	hostnames := make(map[string]string)
	for serviceName := range portAllocation.Ports {
		hostname := caddy.BuildHostname(name, serviceName, projectAlias)
		if hostname == "" {
			continue
		}
		hostnames[serviceName] = hostname
	}

	// Build external hostnames if CF tunnel is configured
	externalHostnames := make(map[string]string)
	if domain := viper.GetString("external_domain"); domain != "" {
		for serviceName := range portAllocation.Ports {
			h := caddy.BuildExternalHostname(name, serviceName, projectAlias, domain)
			if h != "" {
				externalHostnames[serviceName] = h
			}
		}
	}

	// Generate .envrc file
	envData := session.EnvrcData{
		Ports:          portAllocation.Ports,
		Routes:         hostnames,
		ExternalRoutes: externalHostnames,
		Name:           name,
	}
	if err := session.GenerateEnvrc(worktreePath, envData); err != nil {
		return fmt.Errorf("failed to generate .envrc: %w", err)
	}

	// Generate tmuxp config
	tmuxpData := session.TmuxpData{
		Name:           name,
		Path:           worktreePath,
		Ports:          portAllocation.Ports,
		Routes:         hostnames,
		ExternalRoutes: externalHostnames,
	}
	if err := session.GenerateTmuxpConfig(worktreePath, tmuxpData, projectPath); err != nil {
		return fmt.Errorf("failed to generate tmuxp config: %w", err)
	}

	// Update session with hostname information
	if len(hostnames) > 0 {
		if err := store.UpdateSession(name, func(s *session.Session) {
			s.Routes = hostnames
		}); err != nil {
			fmt.Printf("Warning: failed to update session routes: %v\n", err)
		}
	}

	// Sync all Caddy routes (writes config file + reloads)
	if err := syncAllCaddyRoutes(); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}
	if err := syncAllCloudflareRoutes(); err != nil {
		fmt.Printf("Warning: Cloudflare sync failed: %v\n", err)
	}

	fmt.Printf("Created session '%s' at %s\n", name, worktreePath)
	if len(portAllocation.Ports) > 0 {
		fmt.Printf("Allocated ports:")
		for serviceName, port := range portAllocation.Ports {
			fmt.Printf(" %s_PORT=%d", strings.ToUpper(serviceName), port)
		}
		fmt.Printf("\n")
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
