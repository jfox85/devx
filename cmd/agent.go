package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
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
		s := &piagent.MCPServer{M: m, Name: "devx-pi", Version: version.Version, Client: "mcp"}
		return s.Serve(cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.PersistentFlags().BoolVar(&agentJSONFlag, "json", false, "JSON output")
	agentInspectCmd.Flags().IntVar(&agentInspectLines, "lines", 200, "Scrollback lines to include")
	agentReleaseCmd.Flags().BoolVar(&agentReleaseDrop, "drop-queued", false, "Cancel prompts queued while you had control instead of delivering them")
	agentRelaunchCmd.Flags().BoolVar(&agentRelaunchForce, "force", false, "Replace a Pi that still appears to be running")
	agentCmd.AddCommand(agentListCmd, agentStatusCmd, agentAttachCmd, agentInspectCmd, agentTakeoverCmd, agentReleaseCmd, agentRelaunchCmd)
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
	return piagent.NewManager(piagent.NewStore(piAgentStateDir()), piagent.Tmux{}, devxSessionCreator{exe: exe}, cfg), nil
}

// devxSessionCreator creates sessions with the regular `devx session create`
// path so worktrees, ports, routes and metadata match manually created ones.
type devxSessionCreator struct{ exe string }

func (c devxSessionCreator) Exists(name string) (bool, error) {
	store, err := session.LoadSessions()
	if err != nil {
		return false, err
	}
	_, ok := store.GetSession(name)
	return ok, nil
}

func (c devxSessionCreator) Create(name, project string, notBefore time.Time) (piagent.CreatedSession, error) {
	if !session.IsValidSessionName(name) {
		return piagent.CreatedSession{}, fmt.Errorf("invalid session name %q", name)
	}
	store, err := session.LoadSessions()
	if err != nil {
		return piagent.CreatedSession{}, err
	}
	if sess, ok := store.GetSession(name); ok {
		// Adopt only a session created by an interrupted attempt of this
		// same start (after its idempotency record was written).
		if sess.CreatedAt.Before(notBefore) || sess.ProjectAlias != project {
			return piagent.CreatedSession{}, fmt.Errorf("session %q already exists and was not created by this start", name)
		}
		return piagent.CreatedSession{Name: name, Path: sess.Path, Project: sess.ProjectAlias, TmuxName: name}, nil
	}
	args := []string{}
	if cfgFile != "" {
		args = append(args, "--config", cfgFile)
	}
	args = append(args, "session", "create", "--no-tmux", "--target", "host", "--project", project, "--", name)
	child := exec.Command(c.exe, args...)
	child.Env = withoutTmuxEnv(os.Environ())
	var out bytes.Buffer
	child.Stdout, child.Stderr = &out, &out
	if err := child.Run(); err != nil {
		return piagent.CreatedSession{}, fmt.Errorf("devx session create: %w: %s", err, strings.TrimSpace(out.String()))
	}
	store, err = session.LoadSessions()
	if err != nil {
		return piagent.CreatedSession{}, err
	}
	sess, ok := store.GetSession(name)
	if !ok {
		return piagent.CreatedSession{}, fmt.Errorf("session %q missing after create", name)
	}
	if sess.IsContainerized() {
		return piagent.CreatedSession{}, fmt.Errorf("managed Pi agents support host sessions only (session %q uses %s)", name, sess.TargetType())
	}
	return piagent.CreatedSession{Name: name, Path: sess.Path, Project: sess.ProjectAlias, TmuxName: name}, nil
}

func (c devxSessionCreator) EnsureTmux(name string) error {
	store, err := session.LoadSessions()
	if err != nil {
		return err
	}
	sess, ok := store.GetSession(name)
	if !ok {
		return fmt.Errorf("session %q not found", name)
	}
	return target.EnsureTmuxSession(name, sess)
}

func withoutTmuxEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
