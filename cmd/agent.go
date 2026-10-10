package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jfox85/devx/artifactbridge"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// devx agent: the human-facing control surface for MCP-managed Pi agents.
// Takeover and release live here (and in the Pi TUI), never in MCP.

var agentJSONFlag bool

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Inspect and take control of MCP-managed Pi agents",
	Long: `Managed Pi agents run as a normal interactive Pi in a "pi-agent" tmux
window of a DevX session. Use these commands to see them, attach, and take or
release control. While a human has control, prompts queued over MCP wait.

Inside the Pi TUI, typing a message or pressing Escape also takes control;
/devx-release hands control back.`,
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List managed agents with reconciled state",
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		views, err := m.List()
		if err != nil {
			return err
		}
		if agentJSONFlag {
			return writeJSONOut(cmd, views)
		}
		if len(views) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No managed Pi agents.")
			return nil
		}
		for _, v := range views {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  session=%s  state=%s  control=%s(gen %d)  waiting=%d  pane=%s\n",
				v.Agent.ID, v.Agent.DevxSession, v.State, v.Agent.Lease.Holder, v.Agent.Lease.Generation, v.Waiting, v.Agent.Binding.PaneID)
		}
		return nil
	},
}

var agentStatusCmd = &cobra.Command{
	Use:   "status <agent-id>",
	Short: "Show an agent's state, control holder and tasks",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		v, err := m.AgentStatus(args[0])
		if err != nil {
			return err
		}
		tasks, err := m.Store.ListTasks(args[0])
		if err != nil {
			return err
		}
		if agentJSONFlag {
			type taskRow struct {
				ID, State, Note string
				HumanIntervened bool
			}
			rows := make([]taskRow, 0, len(tasks))
			for _, t := range tasks {
				rows = append(rows, taskRow{t.ID, t.State, t.Note, t.HumanIntervened})
			}
			return writeJSONOut(cmd, map[string]any{"agent": v, "tasks": rows})
		}
		a := v.Agent
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Agent:       %s\nSession:     %s\nPi session:  %s\nState:       %s %s\nControl:     %s (generation %d, %s, since %s)\nPane:        %s (window %s)\nAttach:      %s\n",
			a.ID, a.DevxSession, a.PiSessionID, v.State, v.Detail, a.Lease.Holder, a.Lease.Generation, a.Lease.Reason, a.Lease.Since.Local().Format(time.RFC3339), a.Binding.PaneID, a.Binding.WindowID, v.AttachHint)
		for _, t := range tasks {
			fmt.Fprintf(out, "  %s  %-9s %s\n", t.ID, t.State, t.Note)
		}
		return nil
	},
}

var agentAttachCmd = &cobra.Command{
	Use:   "attach <agent-id>",
	Short: "Attach to the agent's Pi window (inspect only; typing takes control)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		v, err := m.AgentStatus(args[0])
		if err != nil {
			return err
		}
		if !v.BindingOK {
			return fmt.Errorf("agent %s binding is %s: %s", args[0], v.State, v.Detail)
		}
		a := v.Agent
		if _, err := m.Tmux.Run("select-window", "-t", a.Binding.WindowID); err != nil {
			return err
		}
		fmt.Printf("Attaching to %s (window %s). Typing a message takes control; /devx-release returns it.\n", a.DevxSession, a.Binding.WindowID)
		store, err := session.LoadSessions()
		if err != nil {
			return err
		}
		sess, ok := store.GetSession(a.DevxSession)
		if !ok {
			return fmt.Errorf("session %q not found", a.DevxSession)
		}
		return readyAttach(store, a.DevxSession, sess)
	},
}

var agentInspectLines int

var agentInspectCmd = &cobra.Command{
	Use:   "inspect <agent-id>",
	Short: "Print the agent's current Pi screen without attaching or taking control",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		out, err := m.Inspect(args[0], agentInspectLines)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), out)
		return nil
	},
}

var agentTakeoverCmd = &cobra.Command{
	Use:   "takeover <agent-id>",
	Short: "Take control: queued MCP prompts stop being delivered until release",
	Long: `Take control of a managed agent. After this returns, no queued MCP prompt
that had not already reached Pi will be delivered until you release. Work that
already reached Pi keeps running in the TUI, where you can interrupt it.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		a, err := m.Takeover(args[0], currentUserLabel())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "You have control of %s (generation %d). Attach with: devx agent attach %s\n", a.ID, a.Lease.Generation, a.ID)
		return nil
	},
}

var agentReleaseDrop bool

var agentReleaseCmd = &cobra.Command{
	Use:   "release <agent-id>",
	Short: "Release control back to managed dispatch",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		a, err := m.Release(args[0], currentUserLabel(), agentReleaseDrop)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Released %s (generation %d). Queued MCP prompts resume when Pi is idle.\n", a.ID, a.Lease.Generation)
		return nil
	},
}

var agentRelaunchForce bool

var agentRelaunchCmd = &cobra.Command{
	Use:   "relaunch <agent-id>",
	Short: "Restart an exited Pi, resuming the same Pi session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		if err := m.Relaunch(args[0], agentRelaunchForce); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Relaunched %s.\n", args[0])
		return nil
	},
}

var (
	agentAdoptPane      string
	agentAdoptPiSession string
)

var agentAdoptCmd = &cobra.Command{
	Use:   "adopt <session> --pane <%id> --pi-session <id>",
	Short: "Register an existing session's running Pi as a managed agent (no restart)",
	Long: `Register the Pi already running in one of your DevX sessions as an
MCP-managed agent, without restarting it or creating anything.

The agent starts under YOUR control, so nothing is delivered yet. Because the
running Pi was started without the DevX bridge, it cannot receive tasks until
it is relaunched once with the bridge, resuming the same conversation:

  1. devx agent adopt <session> --pane %12 --pi-session <pi-session-id>
  2. when that Pi is idle with nothing unsent:  devx agent relaunch <agent-id> --force
  3. in Pi (or with devx agent release <agent-id>):  /devx-release

Only host sessions can be adopted, and only the exact pane given is ever
touched. Find the pane with: tmux list-panes -s -t <session>
Find the Pi session id in Pi with /session.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		r, err := m.Adopt(piagent.AdoptRequest{Session: args[0], PaneID: agentAdoptPane, PiSessionID: agentAdoptPiSession, By: currentUserLabel()})
		if err != nil {
			return err
		}
		if agentJSONFlag {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(r)
		}
		verb := "Adopted"
		if r.Replayed {
			verb = "Already adopted"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s as %s (pane %s). You have control; nothing is delivered yet.\n"+
			"Next: when that Pi is idle, run `devx agent relaunch %s --force` to load the DevX bridge (same conversation), then /devx-release.\n",
			verb, r.Session, r.AgentID, r.Pane, r.AgentID)
		return nil
	},
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Local MCP servers",
}

var mcpPiCmd = &cobra.Command{
	Use:   "pi",
	Short: "Serve the managed Pi agent MCP tools over stdio",
	Long: `Serve Model Context Protocol tools for managed Pi agents over stdin/stdout.
No network listener is opened. Starting agents requires the project to be
listed in pi_mcp.allowed_projects in the DevX config. Example MCP config:

  {"mcpServers": {"devx-pi": {"command": "devx", "args": ["mcp", "pi"]}}}`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		m, err := newAgentManager()
		if err != nil {
			return err
		}
		s := &piagent.MCPServer{M: m, Name: "devx-pi", Version: version.Version, Client: "mcp", Extra: newArtifactBridge(m)}
		return s.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.PersistentFlags().BoolVar(&agentJSONFlag, "json", false, "JSON output")
	agentInspectCmd.Flags().IntVar(&agentInspectLines, "lines", 200, "Scrollback lines to include")
	agentReleaseCmd.Flags().BoolVar(&agentReleaseDrop, "drop-queued", false, "Cancel prompts queued while you had control instead of delivering them")
	agentRelaunchCmd.Flags().BoolVar(&agentRelaunchForce, "force", false, "Replace a Pi that still appears to be running")
	agentAdoptCmd.Flags().StringVar(&agentAdoptPane, "pane", "", "Exact tmux pane id (%N) where the session's Pi runs")
	agentAdoptCmd.Flags().StringVar(&agentAdoptPiSession, "pi-session", "", "Pi session id of that conversation (shown by /session in Pi)")
	_ = agentAdoptCmd.MarkFlagRequired("pane")
	_ = agentAdoptCmd.MarkFlagRequired("pi-session")
	agentCmd.AddCommand(agentListCmd, agentStatusCmd, agentAttachCmd, agentInspectCmd, agentTakeoverCmd, agentReleaseCmd, agentRelaunchCmd, agentAdoptCmd)
	rootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(mcpPiCmd)
}

func writeJSONOut(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func currentUserLabel() string {
	if u := os.Getenv("USER"); u != "" {
		return "cli:" + u
	}
	return "cli"
}

// piAgentStateDir keeps managed-agent state next to sessions.json, outside
// any worktree.
func piAgentStateDir() string {
	return filepath.Join(filepath.Dir(config.GetSessionsPath()), "pi-agents")
}

// newArtifactBridge returns the artifact bridge tool provider. Its policy
// (pi_mcp.allowed_projects and pi_mcp.artifacts) is re-read from the owner's
// global DevX config on every tools/list and tool call, so edits apply
// without restarting this process or the relay; an unreadable or invalid
// config disables the bridge for that call. Project-level .devx configs and
// DEVX_* environment variables never widen it: the bridge reads only the
// owner's global file, or the file named by an explicit --config flag.
func newArtifactBridge(m *piagent.Manager) piagent.ToolProvider {
	path := artifactPolicyPath()
	return artifactbridge.New(artifactbridge.Config{StateDir: filepath.Join(piAgentStateDir(), "artifact-bridge")}, m.Store,
		func() (artifactbridge.Policy, error) { return artifactbridge.LoadPolicyFile(path) })
}

// artifactPolicyPath is the owner's global DevX config file, the same file
// pi_mcp.allowed_projects is administered in.
func artifactPolicyPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "devx", "config.yaml")
}

func newAgentManager() (*piagent.Manager, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cfg := piagent.Config{
		PiCommand:       viper.GetString("pi_mcp.pi_command"),
		PiArgs:          viper.GetStringSlice("pi_mcp.pi_args"),
		PassEnv:         viper.GetStringSlice("pi_mcp.pass_env"),
		AllowedProjects: viper.GetStringSlice("pi_mcp.allowed_projects"),
		DevxExecutable:  exe,
	}
	return piagent.NewManager(piagent.NewStore(piAgentStateDir()), piagent.Tmux{}, localOnlySessionCreator{}, cfg), nil
}

// localOnlySessionCreator creates managed-agent sessions in local-only
// mode: a DevX session record carrying a local_only marker owned by the
// agent, plus a git worktree. It never runs `devx session create`, so no
// service ports, routes, .envrc/.tmuxp.yaml, bootstrap files, project
// template windows (editor Pi, services), Caddy/Cloudflare sync or tunnel
// reload happen. Route builders skip marked sessions forever after.
type localOnlySessionCreator struct{}

func (localOnlySessionCreator) Exists(name string) (bool, error) {
	store, err := session.LoadSessions()
	if err != nil {
		return false, err
	}
	if _, ok := store.GetSession(name); ok {
		return true, nil
	}
	// A same-named tmux session that DevX does not know about is someone
	// else's; treat it as taken.
	return session.TmuxHasSession(name), nil
}

func (localOnlySessionCreator) Create(name, project, agentID string) (piagent.CreatedSession, error) {
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return piagent.CreatedSession{}, err
	}
	p, ok := registry.Projects[project]
	if !ok || p == nil || p.Path == "" {
		return piagent.CreatedSession{}, fmt.Errorf("project %q is not registered", project)
	}
	sess, err := session.CreateLocalOnlySession(session.LocalOnlyRequest{Name: name, ProjectAlias: project, ProjectPath: p.Path, AgentID: agentID})
	if err != nil {
		if errors.Is(err, session.ErrLocalOnlyNotOwned) {
			return piagent.CreatedSession{}, piagent.Denied("%v", err)
		}
		return piagent.CreatedSession{}, err
	}
	return piagent.CreatedSession{Name: name, Path: sess.Path, Project: project, TmuxName: name}, nil
}

// Adopt records agentID on an existing human-created host session (see
// session.AdoptManagedAgent) and returns where it lives. Idempotent for the
// same agent, so relaunch can use it to re-verify ownership.
func (localOnlySessionCreator) Adopt(name, agentID string) (piagent.AdoptedSession, error) {
	sess, err := session.AdoptManagedAgent(name, agentID)
	if err != nil {
		if errors.Is(err, session.ErrAdoptNotAllowed) {
			return piagent.AdoptedSession{}, piagent.Denied("%v", err)
		}
		return piagent.AdoptedSession{}, err
	}
	if !session.TmuxHasSession(name) {
		_ = session.ReleaseManagedAgent(name, agentID)
		return piagent.AdoptedSession{}, fmt.Errorf("session %q has no running tmux session", name)
	}
	return piagent.AdoptedSession{Name: name, Path: sess.Path, Project: sess.ProjectAlias, TmuxName: name}, nil
}

func (localOnlySessionCreator) ReleaseAdoption(name, agentID string) error {
	return session.ReleaseManagedAgent(name, agentID)
}

func (localOnlySessionCreator) EnsureTmux(name, agentID string) error {
	store, err := session.LoadSessions()
	if err != nil {
		return err
	}
	sess, ok := store.GetSession(name)
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}
	// Fail closed: managed agents only ever run in their own local-only
	// session. A missing or foreign marker is a permission denial.
	if !sess.IsLocalOnly() || sess.LocalOnly.AgentID != agentID || sess.LocalOnly.Owner != session.LocalOnlyOwnerPiMCP {
		return piagent.Denied("session %q is not a local-only session owned by agent %s", name, agentID)
	}
	if err := session.EnsureLocalOnlyTmuxSession(name, sess); err != nil {
		if errors.Is(err, session.ErrLocalOnlyNotOwned) {
			return piagent.Denied("%v", err)
		}
		return err
	}
	return nil
}
