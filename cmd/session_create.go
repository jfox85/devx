package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
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
	targetFlag            string
	imageFlag             string
)

func expandUserPath(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

func trustedGatepostRuntimeConfig() target.GatepostRuntimeConfig {
	cfg := target.GatepostRuntimeConfig{}
	if cfgFile != "" {
		cfg.Root = expandUserPath(viper.GetString("gatepost.root"))
		cfg.LogsCommand = viper.GetString("gatepost.logs_command")
		cfg.ProviderBootstrapCommand = viper.GetString("gatepost.provider_bootstrap_command")
		cfg.AuthHome = expandUserPath(viper.GetString("gatepost.auth_home"))
		cfg.RequiredProviders = viper.GetString("gatepost.required_providers")
		return cfg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}
	global := viper.New()
	global.SetConfigFile(filepath.Join(home, ".config", "devx", "config.yaml"))
	if err := global.ReadInConfig(); err != nil {
		return cfg
	}
	cfg.Root = expandUserPath(global.GetString("gatepost.root"))
	cfg.LogsCommand = global.GetString("gatepost.logs_command")
	cfg.ProviderBootstrapCommand = global.GetString("gatepost.provider_bootstrap_command")
	cfg.AuthHome = expandUserPath(global.GetString("gatepost.auth_home"))
	cfg.RequiredProviders = global.GetString("gatepost.required_providers")
	return cfg
}

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
	sessionCreateCmd.Flags().StringVar(&targetFlag, "target", "", "Execution target: host, docker, or gatepost (default from config)")
	sessionCreateCmd.Flags().StringVar(&imageFlag, "image", "", "Docker image for container sessions")
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

	// Resolve target type: flag > project config > global config > "host"
	targetType := targetFlag
	if targetType == "" {
		targetType = viper.GetString("target")
	}
	if targetType == "" {
		targetType = "host"
	}

	// Validate target type early
	if _, err := target.Resolve(targetType); err != nil {
		return err
	}

	// Check Docker availability before any side effects
	if targetType == "docker" || targetType == "gatepost" {
		if err := target.CheckAvailable(); err != nil {
			return err
		}
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
			// If the effective target is gatepost but no container is running,
			// remove stale metadata and fall through to full creation so
			// Docker/networks/secrets are set up properly. The worktree is kept.
			needsGatepost := targetType == "gatepost" && existingSession.Target.ContainerName == ""
			if !needsGatepost && existingSession.Target.Type == "gatepost" && existingSession.Target.ContainerName == "" {
				needsGatepost = true
			}
			if needsGatepost {
				fmt.Printf("Session '%s' exists but has no container — recreating Gatepost runtime\n", name)
				if err := store.RemoveSession(name); err != nil {
					fmt.Printf("Warning: could not remove stale session metadata: %v\n", err)
				}
				sessionExists = false
				// Fall through to full creation.
			} else {
				fmt.Printf("Reusing existing session '%s' at %s\n", name, existingSession.Path)
				// Skip to tmux launch if not disabled
				if !noTmuxFlag {
					if existingSession.Target.Gatepost.Enabled && existingSession.Target.ContainerName != "" {
						if err := session.EnsureTmuxSessionInContainer(name, existingSession.Target.ContainerName, existingSession); err != nil {
							fmt.Printf("Warning: Failed to launch tmux session in container: %v\n", err)
						}
					} else if session.IsTmuxRunning() {
						fmt.Printf("Note: Already inside tmux. Session exists but not launched.\n")
						fmt.Printf("To launch manually: tmuxp load %s/.tmuxp.yaml\n", existingSession.Path)
					} else {
						if err := session.LaunchTmuxSession(existingSession.Path, name); err != nil {
							fmt.Printf("Warning: Failed to launch tmux session: %v\n", err)
						}
					}
				}
				return nil
			}
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

	// If no explicit --target flag, let project config override the global default.
	if targetFlag == "" && cfg.Target != "" {
		targetType = cfg.Target
		if _, err := target.Resolve(targetType); err != nil {
			return fmt.Errorf("invalid target in project config: %w", err)
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
	if err := session.CopyBootstrapFiles(projectPath, worktreePath, cfg.BootstrapFiles); err != nil {
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

	// Override color and display name if flags were provided
	if createColorFlag != "" || createDisplayNameFlag != "" {
		if err := store.UpdateSession(name, func(s *session.Session) {
			if createColorFlag != "" {
				s.Color = createColorFlag
			}
			if createDisplayNameFlag != "" {
				s.DisplayName = createDisplayNameFlag
			}
		}); err != nil {
			fmt.Printf("Warning: failed to set color/display-name: %v\n", err)
		}
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
	// For Docker sessions, use /workspace as the path inside the container.
	// The file is written to the host worktree (mounted at /workspace).
	tmuxpPath := worktreePath
	if targetType == "docker" || targetType == "gatepost" {
		tmuxpPath = "/workspace"
	}
	tmuxpData := session.TmuxpData{
		Name:           name,
		Path:           tmuxpPath,
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

	// Start the target (container for docker, no-op for host)
	tgt, _ := target.Resolve(targetType) // already validated above
	ctx := context.Background()

	// For container targets: ensure the image exists, start the container(s)
	var targetMeta session.TargetMeta
	if targetType == "docker" || targetType == "gatepost" {
		dockerImage := imageFlag
		if targetType == "gatepost" {
			if dockerImage == "" {
				dockerImage = viper.GetString("gatepost.agent_image")
			}
			if dockerImage == "" {
				dockerImage = "gatepost-pi-agent:latest"
			}
		} else {
			if dockerImage == "" {
				dockerImage = viper.GetString("docker.image")
			}
			if dockerImage == "" {
				dockerImage = "devx-session-base:latest"
			}
		}
		if targetType == "docker" && dockerImage == "devx-session-base:latest" && !target.ImageExists(dockerImage) {
			return fmt.Errorf("devx-session-base image not found. Build it first:\n  docker build -t devx-session-base:latest docker/")
		}

		// Build env map for the container
		containerEnv := make(map[string]string)
		for svc, port := range portAllocation.Ports {
			containerEnv[strings.ToUpper(svc)+"_PORT"] = fmt.Sprintf("%d", port)
		}
		for svc, hostname := range hostnames {
			containerEnv[strings.ToUpper(strings.ReplaceAll(svc, "-", "_"))+"_HOST"] = "http://" + hostname
		}
		containerEnv["SESSION_NAME"] = name

		gatepostConfig := target.GatepostRuntimeConfig{}
		if targetType == "gatepost" {
			gatepostConfig = trustedGatepostRuntimeConfig()
		}

		result, err := tgt.Start(ctx, target.StartOpts{
			SessionName:  name,
			WorktreePath: worktreePath,
			HostPorts:    portAllocation.Ports,
			Image:        dockerImage,
			Env:          containerEnv,
			Labels: map[string]string{
				"devx.session": name,
				"devx.project": projectAlias,
			},
			Security:       target.DefaultSecurityOpts(),
			GatepostConfig: gatepostConfig,
		})
		if err != nil {
			return fmt.Errorf("failed to start docker target: %w", err)
		}

		targetMeta = result.Meta

		// Save target metadata
		if err := store.UpdateSession(name, func(s *session.Session) {
			s.Target = targetMeta
		}); err != nil {
			fmt.Printf("Warning: failed to save target metadata: %v\n", err)
		}

		fmt.Printf("Started container '%s'\n", targetMeta.ContainerName)
		if targetMeta.Gatepost.Enabled && targetMeta.Gatepost.LogsURL != "" {
			fmt.Printf("Gatepost Logs: open DevX web and use /api/gatepost/logs?session=%s\n", name)
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
		if targetType == "docker" || targetType == "gatepost" {
			// For Docker: load tmuxp inside the container
			fmt.Println("Loading tmux session inside container...")
			loadCmd := target.ExecInSession(targetMeta, []string{
				"tmuxp", "load", "-d", "/workspace/.tmuxp.yaml", "-s", name,
			}, false)
			if output, err := loadCmd.CombinedOutput(); err != nil {
				fmt.Printf("Warning: Failed to load tmux inside container: %v\n%s\n", err, output)
			}

			// Attach if not already inside tmux
			if !session.IsTmuxRunning() {
				attachCmd := target.ExecInSession(targetMeta, []string{
					"tmux", "attach", "-t", name,
				}, true)
				attachCmd.Stdin = os.Stdin
				attachCmd.Stdout = os.Stdout
				attachCmd.Stderr = os.Stderr
				if err := attachCmd.Run(); err != nil {
					fmt.Printf("Note: Could not attach to tmux session in container: %v\n", err)
				}
			} else {
				fmt.Printf("Note: Already inside tmux. Session created but not attached.\n")
				fmt.Printf("To attach: devx session attach %s\n", name)
			}
		} else {
			// Host: existing tmux behavior
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
