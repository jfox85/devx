package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var (
	instancesApplyPlan string
	instancesRollback  string
	instancesJSON      bool
	instancesPrepare   bool
)

var sessionInstancesCmd = &cobra.Command{
	Use:   "instances",
	Short: "Review and assign stable session instance ids (dry run by default)",
	Long: `Assign a stable instance id to every session record that has none, and
bind each managed Pi agent to the exact session instance it belongs to.

Without flags this is a DRY RUN: it reads the records, prints the plan and a
plan hash, and writes nothing.

  devx session instances                     # review the plan
  devx session instances --apply <plan-hash> # apply exactly that plan
  devx session instances --rollback <dir>    # undo a previous apply

An agent is bound only when the evidence identifies exactly one session
record and nothing contradicts it. Anything ambiguous (a recreated session,
a marker naming another agent, duplicate claimants, shared worktrees,
missing timestamps) is listed with a reason and left unchanged.

--apply re-plans, refuses unless the hash matches the reviewed plan, backs up
sessions.json and every agent record it will touch, journals each write, and
re-validates every step under the record locks. Re-running after an
interrupted apply completes it. Only instance_id (sessions) and
session_instance_id / session_created_at (agents) are written; worktrees,
tmux sessions, Pi conversations, leases and markers are never touched.`,
	Args: cobra.NoArgs,
	RunE: runSessionInstances,
}

func init() {
	sessionCmd.AddCommand(sessionInstancesCmd)
	sessionInstancesCmd.Flags().StringVar(&instancesApplyPlan, "apply", "", "apply the plan with this hash (from a dry run)")
	sessionInstancesCmd.Flags().StringVar(&instancesRollback, "rollback", "", "undo the apply recorded in this backup directory")
	sessionInstancesCmd.Flags().BoolVar(&instancesJSON, "json", false, "print the plan as JSON")
	sessionInstancesCmd.Flags().BoolVar(&instancesPrepare, "prepare", false, "create the private migration key (if missing) and print an applicable dry-run plan; writes no record")
}

func instanceStores(journal func(piagent.JournalEntry) error) piagent.InstanceStores {
	return piagent.InstanceStores{
		MutateSessions: func(fn func(map[string]*session.Session) error) error {
			return (&session.SessionStore{}).Mutate(func(fresh *session.SessionStore) error { return fn(fresh.Sessions) })
		},
		LoadSessions: func() (map[string]*session.Session, error) {
			st, err := session.LoadSessions()
			if err != nil {
				return nil, err
			}
			return st.Sessions, nil
		},
		Agents:  piagent.NewStore(piAgentStateDir()),
		Journal: journal,
	}
}

// currentInstancePlan plans against the live records. Proposed ids are
// derived from the record state with the owner-private key, so the plan hash
// reviewed in a dry run still matches at apply time unless a record changed.
// A dry run never creates the key: without one it shows placeholder ids and
// cannot be applied.
func currentInstancePlan(createKey bool) (*piagent.InstancePlan, bool, error) {
	st, err := session.LoadSessions()
	if err != nil {
		return nil, false, err
	}
	agents, err := piagent.NewStore(piAgentStateDir()).ListAgents()
	if err != nil {
		return nil, false, err
	}
	key, err := instanceKey(createKey)
	if err != nil {
		return nil, false, err
	}
	if key == nil {
		return piagent.PlanInstances(st.Sessions, agents, func(string) string { return "si_<assigned-at-apply>" }), false, nil
	}
	return piagent.PlanInstances(st.Sessions, agents, deterministicIDs(st.Sessions, key)), true, nil
}

func runSessionInstances(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	if instancesRollback != "" {
		return runInstancesRollback(out, instancesRollback)
	}
	plan, keyed, err := currentInstancePlan(instancesApplyPlan != "" || instancesPrepare)
	if err != nil {
		return err
	}
	if instancesApplyPlan == "" {
		return printInstancePlan(out, plan, keyed)
	}
	if instancesApplyPlan != plan.Hash {
		return fmt.Errorf("plan hash %s does not match the current plan %s: the records changed or a different plan was reviewed; re-run the dry run", instancesApplyPlan, plan.Hash)
	}
	if !plan.HasWork() {
		_, _ = fmt.Fprintln(out, "Nothing to apply.")
		return nil
	}
	dir, err := backupInstanceRecords(plan)
	if err != nil {
		return fmt.Errorf("backup failed, nothing written: %w", err)
	}
	jf, err := os.OpenFile(filepath.Join(dir, "journal.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = jf.Close() }()
	journal := func(e piagent.JournalEntry) error {
		b, _ := json.Marshal(e)
		if _, err := jf.Write(append(b, '\n')); err != nil {
			return err
		}
		return jf.Sync()
	}
	if err := piagent.ApplyInstancePlan(plan, instanceStores(journal)); err != nil {
		return fmt.Errorf("apply stopped (backup and journal in %s; re-run the dry run to see what remains, or --rollback %s): %w", dir, dir, err)
	}
	_, _ = fmt.Fprintf(out, "Applied plan %s. Backup and journal: %s\nUndo with: devx session instances --rollback %s\n", plan.Hash, dir, dir)
	after, _, err := currentInstancePlan(false)
	if err == nil && after.HasWork() {
		_, _ = fmt.Fprintln(out, "Warning: records changed during apply; re-run the dry run.")
	}
	return nil
}

// deterministicIDs proposes the same id for the same record state, so the
// plan hash a human reviewed still matches at apply time unless something
// changed. The id is derived from a random per-store salt persisted next to
// sessions.json (never from secrets), so ids are not guessable from names.
func deterministicIDs(sessions map[string]*session.Session, key []byte) func(name string) string {
	return func(name string) string {
		s := sessions[name]
		return session.DeriveInstanceID(key, name, s.Path, s.CreatedAt)
	}
}

// instanceKey returns the owner-private random key used to derive migration
// ids, creating it (0600, exclusive create) on first use. It is not a
// credential: it only keeps derived ids unguessable.
func instanceKey(create bool) ([]byte, error) {
	p := filepath.Join(filepath.Dir(config.GetSessionsPath()), ".instance-key")
	if b, err := os.ReadFile(p); err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("%s is malformed", p)
		}
		return b, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, nil
	}
	b := []byte(session.NewInstanceID()[3:] + session.NewInstanceID()[3:11])
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return instanceKey(false)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return nil, err
	}
	return b, f.Close()
}

func printInstancePlan(out io.Writer, p *piagent.InstancePlan, keyed bool) error {
	if instancesJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	if keyed {
		_, _ = fmt.Fprintf(out, "DRY RUN - nothing written. Plan hash: %s\n\n", p.Hash)
	} else {
		_, _ = fmt.Fprintf(out, "DRY RUN - nothing written. Preview only (no migration key yet): ids are placeholders.\n"+
			"Run `devx session instances --prepare` to create the key and get an applicable plan hash.\n\n")
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SESSION\tACTION\tINSTANCE ID\tNOTE")
	n := 0
	for _, s := range p.Sessions {
		note := s.Reason
		if s.Existing {
			note = "restores the id its agent is bound to"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Session, s.Action, s.InstanceID, note)
		n++
	}
	if n == 0 {
		_, _ = fmt.Fprintln(tw, "(every session already has an instance id)\t\t\t")
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out)
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "AGENT\tSESSION\tACTION\tMARKER\tSESSION-AGENT CREATED\tREASON")
	for _, a := range p.Agents {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", a.AgentID, a.Session, a.Action, a.Marker, a.SessionVsAgent, a.Reason)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "\nSummary: %v\n", p.Summary)
	for _, note := range p.Notes {
		_, _ = fmt.Fprintln(out, "- "+note)
	}
	if p.HasWork() && keyed {
		_, _ = fmt.Fprintf(out, "\nTo apply exactly this plan: devx session instances --apply %s\n", p.Hash)
	} else if p.HasWork() {
		_, _ = fmt.Fprintln(out, "\nNot applicable yet (preview).")
	} else {
		_, _ = fmt.Fprintln(out, "\nNothing to apply.")
	}
	return nil
}

// backupInstanceRecords copies sessions.json and every agent record the plan
// touches into a fresh 0700 directory before anything is written.
func backupInstanceRecords(p *piagent.InstancePlan) (string, error) {
	base := filepath.Join(filepath.Dir(config.GetSessionsPath()), "backups")
	dir := filepath.Join(base, "session-instances-"+time.Now().UTC().Format("20060102T150405Z")+"-"+p.Hash)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	copyFile := func(src, dst string) error {
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o600)
	}
	if err := copyFile(config.GetSessionsPath(), filepath.Join(dir, "sessions.json")); err != nil {
		return "", err
	}
	store := piagent.NewStore(piAgentStateDir())
	for _, a := range p.Agents {
		if a.Action != piagent.ActionBindAgent {
			continue
		}
		src := filepath.Join(store.AgentDir(a.AgentID), "agent.json")
		if err := copyFile(src, filepath.Join(dir, "agents", a.AgentID, "agent.json")); err != nil {
			return "", err
		}
	}
	plan, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), plan, 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

func runInstancesRollback(out io.Writer, dir string) error {
	f, err := os.Open(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		return fmt.Errorf("no journal in %s: %w", dir, err)
	}
	defer func() { _ = f.Close() }()
	var entries []piagent.JournalEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e piagent.JournalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// A torn last line from a crash mid-append: that write did not
			// happen (journal is written before the record), stop here.
			break
		}
		entries = append(entries, e)
	}
	n, skipped, err := piagent.RollbackInstances(entries, instanceStores(nil))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Rolled back %d change(s) from %s.\n", n, dir)
	for _, s := range skipped {
		_, _ = fmt.Fprintln(out, "  left unchanged: "+s)
	}
	return nil
}
