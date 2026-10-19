package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
	"github.com/spf13/cobra"
)

var (
	recoverSince         time.Duration
	recoverAll           bool
	recoverDryRun        bool
	recoverJSON          bool
	recoverIncludeExited bool
)

var sessionRecoverCmd = &cobra.Command{
	Use:   "recover [session...]",
	Short: "Recreate tmux sessions and relaunch crashed Pi agents after a crash or reboot",
	Long: `Bring DevX sessions back after the tmux server died (machine crash, reboot,
or tmux kill-server). Worktrees, session metadata and Pi conversations live on
disk and survive; only tmux and the processes inside it are lost.

recover:
  - recreates each selected session's tmux environment from its tmuxp template
    (the same path 'devx session attach' uses), and
  - relaunches managed Pi agents that crashed, resuming the same Pi
    conversation (the same path 'devx agent relaunch' uses).

Sessions are selected when named explicitly, with --all, or when they were
active within --since (last attached or updated). An agent is relaunched only
when its Pi is confirmed gone, its last bridge heartbeat is within --since,
and its tmux pane was lost too. A Pi that exited while its pane stayed open
(quit by hand, or crashed on its own) is left alone unless --include-exited
is given. Adopted agents are never touched:
their original pane is gone, so they must be adopted again.

Sessions that already have a running tmux session are left alone.

  devx session recover --dry-run        # show what would be recovered
  devx session recover                  # recover sessions active in the last 24h
  devx session recover --since 72h      # widen the window
  devx session recover my-feature       # recover specific sessions
  devx session recover --all            # every session with a worktree`,
	RunE: runSessionRecover,
}

func init() {
	sessionCmd.AddCommand(sessionRecoverCmd)
	sessionRecoverCmd.Flags().DurationVar(&recoverSince, "since", 24*time.Hour, "only recover sessions and agents active within this window")
	sessionRecoverCmd.Flags().BoolVar(&recoverAll, "all", false, "recover every session regardless of recent activity")
	sessionRecoverCmd.Flags().BoolVar(&recoverDryRun, "dry-run", false, "print the plan without changing anything")
	sessionRecoverCmd.Flags().BoolVar(&recoverJSON, "json", false, "print the plan/result as JSON")
	sessionRecoverCmd.Flags().BoolVar(&recoverIncludeExited, "include-exited", false, "also relaunch Pi agents that exited while their pane stayed open")
}

// Recover actions.
const (
	recoverActRecreate = "recreate"  // tmux session missing; recreate it
	recoverActRelaunch = "relaunch"  // crashed Pi agent; relaunch it
	recoverActRunning  = "running"   // tmux session already alive
	recoverActViaAgent = "via_agent" // local-only session; its agent relaunch recreates it
	recoverActSkip     = "skip"
)

// Recover outcomes (filled in after execution).
const (
	recoverOutDone    = "done"
	recoverOutFailed  = "failed"
	recoverOutSkipped = "skipped"
)

type recoverAgentItem struct {
	AgentID string `json:"agent_id"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Error   string `json:"error,omitempty"`
}

type recoverSessionItem struct {
	Session string             `json:"session"`
	Tmux    string             `json:"tmux"`
	Reason  string             `json:"reason,omitempty"`
	Outcome string             `json:"outcome,omitempty"`
	Error   string             `json:"error,omitempty"`
	Agents  []recoverAgentItem `json:"agents,omitempty"`
}

type recoverPlan struct {
	DryRun  bool                 `json:"dry_run"`
	Since   string               `json:"since"`
	Items   []recoverSessionItem `json:"sessions"`
	Ignored int                  `json:"ignored_inactive_sessions"`
}

type recoverOptions struct {
	Names         []string
	All           bool
	Cutoff        time.Time
	IncludeExited bool
}

// recoverDeps isolates every side effect so planning and execution are
// testable without tmux, Docker or Pi.
type recoverDeps struct {
	// liveTmux returns the names of tmux sessions currently running. An
	// error means tmux could not be queried (not "no server").
	liveTmux      func() (map[string]bool, error)
	targetRunning func(meta session.TargetMeta) bool
	pathExists    func(path string) bool
	ensureTmux    func(name string, sess *session.Session) error
	agents        func() ([]*piagent.AgentView, error)
	agentStatus   func(id string) (*piagent.AgentView, error)
	relaunch      func(id string) error
}

func runSessionRecover(cmd *cobra.Command, args []string) error {
	if recoverSince <= 0 {
		return fmt.Errorf("--since must be positive")
	}
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	m, err := newAgentManager()
	if err != nil {
		return err
	}
	deps := recoverDeps{
		liveTmux:      liveTmuxSessions,
		targetRunning: target.IsRunning,
		pathExists:    func(p string) bool { _, err := os.Stat(p); return err == nil },
		ensureTmux:    target.EnsureTmuxSession,
		agents:        m.List,
		agentStatus:   m.AgentStatus,
		relaunch:      func(id string) error { return m.Relaunch(id, false) },
	}
	opts := recoverOptions{Names: args, All: recoverAll, Cutoff: time.Now().Add(-recoverSince), IncludeExited: recoverIncludeExited}
	plan, err := planRecover(store.Sessions, deps, opts)
	if err != nil {
		return err
	}
	plan.Since = recoverSince.String()
	plan.DryRun = recoverDryRun
	if !recoverDryRun {
		executeRecover(plan, store.Sessions, deps, opts)
	}
	out := cmd.OutOrStdout()
	if recoverJSON {
		if err := writeJSONOut(cmd, plan); err != nil {
			return err
		}
	} else {
		printRecoverPlan(out, plan)
	}
	if failed := recoverFailures(plan); failed > 0 {
		return fmt.Errorf("%d recovery step(s) failed", failed)
	}
	return nil
}

// liveTmuxSessions lists running tmux sessions. "No server" is an empty set
// (that is exactly the post-crash state); any other failure is an error so
// recover never mistakes an unreachable server for missing sessions.
func liveTmuxSessions() (map[string]bool, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").CombinedOutput()
	live := map[string]bool{}
	if err != nil {
		if tmuxNoServer(string(out)) {
			return live, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			live[line] = true
		}
	}
	return live, nil
}

func tmuxNoServer(msg string) bool {
	return strings.Contains(msg, "no server running") ||
		(strings.Contains(msg, "error connecting to") && strings.Contains(msg, "No such file or directory"))
}

// sessionLastActive is the newest activity timestamp DevX records for a session.
func sessionLastActive(s *session.Session) time.Time {
	t := s.LastAttached
	if s.UpdatedAt.After(t) {
		t = s.UpdatedAt
	}
	return t
}

// agentRecoverDecision decides whether a managed agent crashed recently
// enough to relaunch. Only a confirmed dead pane or Pi qualifies: unknown
// tmux state, live agents and adopted agents are never touched.
//
// The bridge's "stopped" mode is not a reliable sign of an intentional quit:
// a reboot or tmux kill-server hangs Pi up and its shutdown hook writes
// "stopped" too. The pane tells them apart: after a normal Pi exit the
// launch script keeps the pane open for inspection, while a lost tmux server
// takes the pane with it.
func agentRecoverDecision(v *piagent.AgentView, cutoff time.Time, includeExited bool) (bool, string) {
	a := v.Agent
	switch {
	case a.RetiredAt != nil:
		return false, "retired"
	case a.Adopted:
		return false, fmt.Sprintf("adopted agent (state %s); recover it with devx agent adopt / relaunch by hand", v.State)
	case v.State != piagent.AgentPaneExited:
		if d := strings.TrimSpace(v.Detail); d != "" {
			return false, fmt.Sprintf("state %s: %s", v.State, d)
		}
		return false, "state " + v.State
	case v.Bridge == nil:
		return false, "no bridge heartbeat recorded"
	case v.PaneAlive && !includeExited:
		return false, "Pi exited but its pane is still open, so tmux was not lost (devx agent relaunch it, or use --include-exited)"
	case v.Bridge.Heartbeat.Before(cutoff):
		return false, fmt.Sprintf("last seen %s, outside --since window", v.Bridge.Heartbeat.Local().Format(time.RFC3339))
	}
	return true, "Pi is gone (last seen " + v.Bridge.Heartbeat.Local().Format(time.RFC3339) + ")"
}

func ownsLocalOnlySession(sess *session.Session, agentID string) bool {
	return sess.IsLocalOnly() && sess.LocalOnly.AgentID == agentID && sess.LocalOnly.Owner == session.LocalOnlyOwnerPiMCP
}

func planRecover(sessions map[string]*session.Session, deps recoverDeps, opts recoverOptions) (*recoverPlan, error) {
	named := map[string]bool{}
	for _, n := range opts.Names {
		if _, ok := sessions[n]; !ok {
			return nil, fmt.Errorf("session %q not found", n)
		}
		named[n] = true
	}
	live, err := deps.liveTmux()
	if err != nil {
		return nil, fmt.Errorf("cannot query tmux; nothing was changed: %w", err)
	}
	views, err := deps.agents()
	if err != nil {
		return nil, fmt.Errorf("list managed agents: %w", err)
	}
	bySession := map[string][]*piagent.AgentView{}
	for _, v := range views {
		if v.Agent.RetiredAt != nil {
			continue
		}
		bySession[v.Agent.DevxSession] = append(bySession[v.Agent.DevxSession], v)
	}

	names := make([]string, 0, len(sessions))
	for n := range sessions {
		names = append(names, n)
	}
	sort.Strings(names)

	plan := &recoverPlan{}
	for _, name := range names {
		sess := sessions[name]
		if len(named) > 0 && !named[name] {
			continue
		}
		var agents []recoverAgentItem
		relaunching := false
		for _, v := range bySession[name] {
			ok, reason := agentRecoverDecision(v, opts.Cutoff, opts.IncludeExited)
			if ok && !ownsLocalOnlySession(sess, v.Agent.ID) {
				// Relaunch would be denied: managed agents only run in their
				// own local-only session.
				ok, reason = false, "session has no local-only marker for this agent (see devx session markers)"
			}
			act := recoverActSkip
			if ok {
				act, relaunching = recoverActRelaunch, true
			}
			agents = append(agents, recoverAgentItem{AgentID: v.Agent.ID, Action: act, Reason: reason})
		}
		selected := named[name] || opts.All || relaunching || !sessionLastActive(sess).Before(opts.Cutoff)
		if !selected {
			plan.Ignored++
			continue
		}
		item := recoverSessionItem{Session: name, Agents: agents}
		switch {
		case live[name]:
			item.Tmux, item.Reason = recoverActRunning, "tmux session is running"
		case !deps.pathExists(sess.Path):
			item.Tmux, item.Reason = recoverActSkip, "worktree "+sess.Path+" is missing"
			// Relaunch would fail the same way; do not try.
			for i := range item.Agents {
				if item.Agents[i].Action == recoverActRelaunch {
					item.Agents[i].Action, item.Agents[i].Reason = recoverActSkip, "worktree is missing"
				}
			}
		case sess.IsLocalOnly():
			// A local-only session holds only its agent's Pi window; the
			// agent relaunch recreates it with the correct ownership tag.
			if relaunching {
				item.Tmux, item.Reason = recoverActViaAgent, "local-only agent session; recreated by its agent relaunch"
			} else {
				item.Tmux, item.Reason = recoverActSkip, "local-only agent session with no agent to relaunch"
			}
		case sess.IsContainerized() && !deps.targetRunning(sess.Target):
			item.Tmux, item.Reason = recoverActSkip, fmt.Sprintf("%s runtime %s is not running; start it first", sess.TargetType(), target.RuntimeName(sess.Target))
		case !sess.IsContainerized() && !deps.pathExists(filepath.Join(sess.Path, ".tmuxp.yaml")):
			item.Tmux, item.Reason = recoverActSkip, "no .tmuxp.yaml in worktree; nothing to recreate from"
		default:
			item.Tmux, item.Reason = recoverActRecreate, "tmux session is missing"
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

func executeRecover(plan *recoverPlan, sessions map[string]*session.Session, deps recoverDeps, opts recoverOptions) {
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Tmux == recoverActRecreate {
			if err := deps.ensureTmux(item.Session, sessions[item.Session]); err != nil {
				item.Outcome, item.Error = recoverOutFailed, err.Error()
				// Agents in this session would land in a broken session.
				for j := range item.Agents {
					if item.Agents[j].Action == recoverActRelaunch {
						item.Agents[j].Outcome, item.Agents[j].Error = recoverOutSkipped, "session tmux could not be recreated"
					}
				}
				continue
			}
			item.Outcome = recoverOutDone
		}
		for j := range item.Agents {
			ag := &item.Agents[j]
			if ag.Action != recoverActRelaunch {
				continue
			}
			// Re-check right before acting: another recover, a human or
			// the MCP server may have relaunched it since planning.
			v, err := deps.agentStatus(ag.AgentID)
			if err != nil {
				ag.Outcome, ag.Error = recoverOutFailed, err.Error()
				continue
			}
			if ok, reason := agentRecoverDecision(v, opts.Cutoff, opts.IncludeExited); !ok {
				ag.Outcome, ag.Error = recoverOutSkipped, "no longer needs recovery: "+reason
				continue
			}
			if err := deps.relaunch(ag.AgentID); err != nil {
				ag.Outcome, ag.Error = recoverOutFailed, err.Error()
				continue
			}
			ag.Outcome = recoverOutDone
		}
	}
}

func recoverFailures(plan *recoverPlan) int {
	n := 0
	for _, it := range plan.Items {
		if it.Outcome == recoverOutFailed {
			n++
		}
		for _, a := range it.Agents {
			if a.Outcome == recoverOutFailed {
				n++
			}
		}
	}
	return n
}

func printRecoverPlan(w io.Writer, plan *recoverPlan) {
	if plan.DryRun {
		fmt.Fprintf(w, "Dry run: nothing was changed. Window: --since %s\n\n", plan.Since)
	}
	if len(plan.Items) == 0 {
		fmt.Fprintf(w, "No sessions to recover (%d inactive session(s) outside --since %s; use --all or name them).\n", plan.Ignored, plan.Since)
		return
	}
	verb := map[string]string{recoverActRecreate: "recreate tmux", recoverActRunning: "running", recoverActViaAgent: "via agent", recoverActSkip: "skip"}
	for _, it := range plan.Items {
		fmt.Fprintf(w, "%s: %s — %s%s\n", it.Session, verb[it.Tmux], it.Reason, recoverOutcomeSuffix(it.Outcome, it.Error))
		for _, a := range it.Agents {
			act := "skip"
			if a.Action == recoverActRelaunch {
				act = "relaunch"
			}
			fmt.Fprintf(w, "  agent %s: %s — %s%s\n", a.AgentID, act, a.Reason, recoverOutcomeSuffix(a.Outcome, a.Error))
		}
	}
	if plan.Ignored > 0 {
		fmt.Fprintf(w, "\n%d inactive session(s) outside --since %s were not considered (use --all or name them).\n", plan.Ignored, plan.Since)
	}
}

func recoverOutcomeSuffix(outcome, errMsg string) string {
	switch outcome {
	case "":
		return ""
	case recoverOutDone:
		return " [done]"
	default:
		return fmt.Sprintf(" [%s: %s]", outcome, errMsg)
	}
}
