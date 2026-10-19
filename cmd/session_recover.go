package cmd

import (
	"errors"
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

recover (--dry-run changes no session, agent or task state):
  - recreates each selected session's tmux environment from its tmuxp template
    (the same path 'devx session attach' uses), and
  - relaunches managed Pi agents that crashed, resuming the same Pi
    conversation (the same path 'devx agent relaunch' uses).

Sessions are selected when named explicitly, with --all, or when they were
created or last attached within --since. An agent is relaunched only
when its Pi is confirmed gone, its last bridge heartbeat is within --since,
and its tmux pane was lost too. A Pi that exited while its pane stayed open
(quit by hand, or crashed on its own) is left alone unless --include-exited
is given. Adopted agents are never touched:
their original pane is gone, so they must be adopted again.

tmux is never recreated for a session that is still running (crashed agents
inside it are still relaunched), and a running session is never killed or
modified. Recreating a session runs its worktree's .tmuxp.yaml exactly as
'devx session attach' would, so --all runs every selected template; review
the --dry-run output first.

  devx session recover --dry-run        # show what would be recovered
  devx session recover                  # recover sessions active in the last 24h
  devx session recover --since 72h      # widen the window
  devx session recover my-feature       # recover specific sessions
  devx session recover --all            # every session with a worktree`,
	RunE:         runSessionRecover,
	SilenceUsage: true,
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
	Session string `json:"session,omitempty"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	Error   string `json:"error,omitempty"`
	// launchCount is the agent's launch count when it was judged crashed;
	// the relaunch happens only if nobody launched it since.
	launchCount int
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
	// Unmatched lists agents (not retired) whose DevX session record is
	// gone or whose status could not be read; they are never relaunched.
	Unmatched []recoverAgentItem `json:"unmatched_agents,omitempty"`
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
	// agents lists managed agent records; peek reads one agent's status
	// without reconciling task state, so planning (and --dry-run) write
	// nothing.
	agents func() ([]*piagent.Agent, map[string]error, error)
	peek   func(id string) (*piagent.AgentView, error)
	// owns reports why agent a may not run in sess (nil if it may): the
	// same rule the relaunch itself enforces.
	owns func(name string, sess *session.Session, a *piagent.Agent) error
	// launchPolicyErr is non-nil when every relaunch would be refused.
	launchPolicyErr error
	// relaunch relaunches the agent only if its launch count is unchanged.
	relaunch func(id string, launchCount int) error
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
		agents:        m.Store.ListAgentsLenient,
		peek:          m.PeekStatus,
		owns:          managedSessionOwnedBy,
		relaunch:      m.RelaunchIfUnchanged,
	}
	if m.Config.LaunchPolicyErr != nil {
		deps.launchPolicyErr = fmt.Errorf("the pi_mcp launch policy in the owner DevX config is unusable: %v", m.Config.LaunchPolicyErr)
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

// agentRecoverDecision decides whether a managed agent crashed recently
// enough to relaunch. Only a confirmed dead Pi in a lost pane qualifies:
// unknown tmux state, live agents and adopted agents are never touched.
//
// The bridge's "stopped" mode is not a reliable sign of an intentional quit:
// a reboot or tmux kill-server hangs Pi up and its shutdown hook writes
// "stopped" too. The pane tells them apart: after a normal Pi exit the
// launch script keeps the pane open for inspection, while a lost tmux server
// takes the pane with it. A restarted server can hand the old pane id to an
// unrelated pane (PaneForeign); that pane is lost too.
func agentRecoverDecision(v *piagent.AgentView, cutoff time.Time, includeExited bool) (bool, string) {
	a := v.Agent
	foreignLost := v.PaneForeign && v.BridgeDead && v.State == piagent.AgentStaleBind
	switch {
	case a.RetiredAt != nil:
		return false, "retired"
	case a.Adopted:
		return false, fmt.Sprintf("adopted agent (state %s); recover it with devx agent adopt / relaunch by hand", v.State)
	case v.State != piagent.AgentPaneExited && !foreignLost:
		if d := strings.TrimSpace(v.Detail); d != "" {
			return false, fmt.Sprintf("state %s: %s", v.State, d)
		}
		return false, "state " + v.State
	case v.Bridge == nil:
		return false, "no bridge heartbeat recorded"
	case v.PaneForeign && !v.BridgeDead:
		return false, "its pane id now belongs to another pane and its Pi is not confirmed gone"
	case v.PaneAlive && !v.PaneForeign && !includeExited:
		return false, "Pi exited but its pane is still open, so tmux was not lost (devx agent relaunch it, or use --include-exited)"
	case v.Bridge.Heartbeat.Before(cutoff):
		return false, fmt.Sprintf("last seen %s, outside --since window", v.Bridge.Heartbeat.Local().Format(time.RFC3339))
	}
	return true, "Pi is gone (last seen " + v.Bridge.Heartbeat.Local().Format(time.RFC3339) + ")"
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
	agents, unreadable, err := deps.agents()
	if err != nil {
		return nil, fmt.Errorf("list managed agents: %w", err)
	}
	plan := &recoverPlan{}
	badIDs := make([]string, 0, len(unreadable))
	for id := range unreadable {
		badIDs = append(badIDs, id)
	}
	sort.Strings(badIDs)
	for _, id := range badIDs {
		if len(opts.Names) > 0 {
			break // unknown session: cannot tell if it was named; reported only without names
		}
		// One unreadable agent must not block recovering the rest.
		plan.Unmatched = append(plan.Unmatched, recoverAgentItem{AgentID: id, Action: recoverActSkip,
			Reason: "agent record unreadable", Outcome: recoverOutFailed, Error: unreadable[id].Error()})
	}
	bySession := map[string][]*piagent.AgentView{}
	for _, a := range agents {
		if a.RetiredAt != nil {
			continue
		}
		if len(named) > 0 && !named[a.DevxSession] {
			continue
		}
		v, err := deps.peek(a.ID)
		if err != nil {
			// One unreadable agent must not block recovering the rest.
			plan.Unmatched = append(plan.Unmatched, recoverAgentItem{AgentID: a.ID, Session: a.DevxSession, Action: recoverActSkip,
				Reason: "status unavailable", Outcome: recoverOutFailed, Error: err.Error()})
			continue
		}
		if _, ok := sessions[a.DevxSession]; !ok {
			if crashed, _ := agentRecoverDecision(v, opts.Cutoff, opts.IncludeExited); crashed {
				plan.Unmatched = append(plan.Unmatched, recoverAgentItem{AgentID: a.ID, Session: a.DevxSession, Action: recoverActSkip,
					Reason: "crashed, but its DevX session record is gone"})
			}
			continue
		}
		bySession[a.DevxSession] = append(bySession[a.DevxSession], v)
	}

	names := make([]string, 0, len(sessions))
	for n := range sessions {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		sess := sessions[name]
		if len(named) > 0 && !named[name] {
			continue
		}
		var agents []recoverAgentItem
		relaunching := false
		for _, v := range bySession[name] {
			ok, reason := agentRecoverDecision(v, opts.Cutoff, opts.IncludeExited)
			if ok {
				// The relaunch would be refused; say why up front.
				if err := deps.owns(name, sess, v.Agent); err != nil {
					ok, reason = false, err.Error()+" (see devx session markers / instances)"
				} else if deps.launchPolicyErr != nil {
					ok, reason = false, deps.launchPolicyErr.Error()
				}
			}
			act := recoverActSkip
			if ok {
				act, relaunching = recoverActRelaunch, true
			}
			agents = append(agents, recoverAgentItem{AgentID: v.Agent.ID, Action: act, Reason: reason, launchCount: v.Agent.LaunchCount})
		}
		activity, _ := sess.ActivityAt()
		selected := named[name] || opts.All || relaunching || !activity.Before(opts.Cutoff)
		if !selected {
			plan.Ignored++
			continue
		}
		item := recoverSessionItem{Session: name, Agents: agents}
		switch {
		case live[name]:
			item.Tmux, item.Reason = recoverActRunning, "tmux session is running"
		case live[name+"-web"]:
			// The web viewer is grouped with the session and shares its
			// windows: they (and the processes in them) are still running.
			// Recreating the session would start the template a second time.
			item.Tmux, item.Reason = recoverActSkip, fmt.Sprintf("its web viewer session %q is still running the session's windows; attach from the web UI, or kill it and recover again", name+"-web")
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
				item.Tmux, item.Reason = recoverActSkip, "local-only agent session with no agent eligible for relaunch"
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

// executeRecover relaunches agents first, then recreates tmux sessions, so
// sessions created by tmuxp never take over panes a relaunch is about to
// look at. tmux is queried again first: a session started (by a human, the
// web UI or another recover) since planning is left alone.
func executeRecover(plan *recoverPlan, sessions map[string]*session.Session, deps recoverDeps, opts recoverOptions) {
	for i := range plan.Items {
		for j := range plan.Items[i].Agents {
			ag := &plan.Items[i].Agents[j]
			if ag.Action != recoverActRelaunch {
				continue
			}
			// Re-check right before acting: another recover, a human or
			// the MCP server may have relaunched it since planning. The
			// relaunch itself re-checks the launch count under the agent's
			// launch lock, which closes the remaining race.
			v, err := deps.peek(ag.AgentID)
			if err != nil {
				ag.Outcome, ag.Error = recoverOutFailed, err.Error()
				continue
			}
			if ok, reason := agentRecoverDecision(v, opts.Cutoff, opts.IncludeExited); !ok || v.Agent.LaunchCount != ag.launchCount {
				if ok {
					reason = "it was relaunched since planning"
				}
				ag.Outcome, ag.Error = recoverOutSkipped, "no longer needs recovery: "+reason
				continue
			}
			if err := deps.relaunch(ag.AgentID, ag.launchCount); err != nil {
				if errors.Is(err, piagent.ErrLaunchedSinceCheck) {
					ag.Outcome, ag.Error = recoverOutSkipped, "no longer needs recovery: it was relaunched since planning"
					continue
				}
				ag.Outcome, ag.Error = recoverOutFailed, err.Error()
				continue
			}
			ag.Outcome = recoverOutDone
		}
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if item.Tmux != recoverActRecreate {
			continue
		}
		// Query right before each recreation: an earlier one may have run
		// long enough for this session (or its viewer) to appear.
		live, liveErr := deps.liveTmux()
		switch {
		case liveErr != nil:
			item.Outcome, item.Error = recoverOutFailed, "cannot query tmux; not recreated: "+liveErr.Error()
		case live[item.Session] || live[item.Session+"-web"]:
			item.Outcome, item.Error = recoverOutSkipped, "a tmux session appeared since planning; left alone"
		default:
			if err := deps.ensureTmux(item.Session, sessions[item.Session]); err != nil {
				item.Outcome, item.Error = recoverOutFailed, err.Error()
				continue
			}
			item.Outcome = recoverOutDone
		}
	}
}

func recoverFailures(plan *recoverPlan) int {
	n := 0
	for _, a := range plan.Unmatched {
		if a.Outcome == recoverOutFailed {
			n++
		}
	}
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
	defer func() {
		if len(plan.Unmatched) == 0 {
			return
		}
		fmt.Fprintln(w, "\nAgents not recovered:")
		for _, a := range plan.Unmatched {
			where := ""
			if a.Session != "" {
				where = " (session " + a.Session + ")"
			}
			fmt.Fprintf(w, "  agent %s%s: %s%s\n", a.AgentID, where, a.Reason, recoverOutcomeSuffix(a.Outcome, a.Error))
		}
	}()
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
