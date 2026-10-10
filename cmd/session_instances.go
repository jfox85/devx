package cmd

import (
	"bufio"
	"encoding/json"
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
	instancesApplyPlan  string
	instancesRollback   string
	instancesJSON       bool
	instancesForceOlder bool
	instancesConfirm    []string
)

var sessionInstancesCmd = &cobra.Command{
	Use:   "instances",
	Short: "Review and assign stable session instance ids (dry run by default)",
	Long: `Assign a stable instance id to every session record that has none, and
bind each managed Pi agent to the exact session instance it belongs to.

Without --apply/--rollback this is a READ-ONLY DRY RUN: it reads the
records, prints the exact plan and its hash, and writes nothing at all (no
key, no lock, no backup). The ids it shows are the ids --apply writes.

  devx session instances                                     # review the plan
  devx session instances --confirm <agent-id> ...            # plan that also binds confirmed candidates
  devx session instances --apply <plan-hash> [--confirm ...] # apply exactly that plan
  devx session instances --rollback <dir>                    # undo a previous apply

An agent is bound automatically only when its session carries a marker
naming it and nothing contradicts it (ordering, path, project, claimants,
worktree, existing ids). Agents whose session has NO marker are listed as
candidates: name, path and creation-time ordering alone are ambiguous, so
they are bound only if the owner confirms each one with --confirm (recorded
as basis owner_confirmed). Anything else ambiguous is listed with a reason
and left unchanged.

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
	sessionInstancesCmd.Flags().BoolVar(&instancesForceOlder, "force-older", false, "allow --rollback of a run that is not the newest")
	sessionInstancesCmd.Flags().StringSliceVar(&instancesConfirm, "confirm", nil, "owner-confirmed candidate agent ids (no marker) to bind; part of the plan hash")
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

// currentInstancePlan plans against the live records. It only reads: proposed
// ids are a deterministic hash of each record's identity, so the plan hash
// reviewed in a dry run still matches at apply time unless a record (or the
// confirmation set) changed.
func currentInstancePlan() (*piagent.InstancePlan, error) {
	st, err := session.LoadSessions()
	if err != nil {
		return nil, err
	}
	store := piagent.NewStore(piAgentStateDir())
	agents, err := store.ListAgents()
	if err != nil {
		return nil, err
	}
	return piagent.PlanInstancesWithOptions(st.Sessions, agents, derivedIDs(st.Sessions),
		piagent.PlanOptions{Recorded: store.RecordedBinding, Confirmed: instancesConfirm}), nil
}

func runSessionInstances(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	if instancesRollback != "" {
		return runInstancesRollback(out, instancesRollback)
	}
	plan, err := currentInstancePlan()
	if err != nil {
		return err
	}
	if instancesApplyPlan == "" {
		return printInstancePlan(out, plan)
	}
	if len(plan.UnknownConfirmations) > 0 {
		return fmt.Errorf("--confirm names agents that are not candidates in this plan: %s; re-run the dry run", strings.Join(plan.UnknownConfirmations, ", "))
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
	after, err := currentInstancePlan()
	if err == nil && after.HasWork() {
		_, _ = fmt.Fprintln(out, "Warning: records changed during apply; re-run the dry run.")
	}
	return nil
}

// derivedIDs proposes, for a record without an id, the deterministic id of
// that exact record (session.DeriveInstanceID): same record, same id.
func derivedIDs(sessions map[string]*session.Session) func(name string) string {
	return func(name string) string {
		s := sessions[name]
		return session.DeriveInstanceID(name, s.Path, s.CreatedAt)
	}
}

func printInstancePlan(out io.Writer, p *piagent.InstancePlan) error {
	if instancesJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	_, _ = fmt.Fprintf(out, "DRY RUN - read-only, nothing written. Plan hash: %s\n", p.Hash)
	if len(p.OwnerConfirmed) > 0 {
		_, _ = fmt.Fprintf(out, "Owner-confirmed candidates in this plan: %s\n", strings.Join(p.OwnerConfirmed, ", "))
	}
	if len(p.UnknownConfirmations) > 0 {
		_, _ = fmt.Fprintf(out, "NOT CANDIDATES (cannot be confirmed; plan not applicable): %s\n", strings.Join(p.UnknownConfirmations, ", "))
	}
	_, _ = fmt.Fprintln(out)
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
	_, _ = fmt.Fprintln(tw, "AGENT\tSESSION\tACTION\tBASIS\tMARKER\tSESSION-AGENT CREATED\tREASON")
	for _, a := range p.Agents {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", a.AgentID, a.Session, a.Action, a.Basis, a.Marker, a.SessionVsAgent, a.Reason)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "\nSummary: %v\n", p.Summary)
	for _, note := range p.Notes {
		_, _ = fmt.Fprintln(out, "- "+note)
	}
	confirm := ""
	for _, id := range p.OwnerConfirmed {
		confirm += " --confirm " + id
	}
	if p.HasWork() && len(p.UnknownConfirmations) == 0 {
		_, _ = fmt.Fprintf(out, "\nTo apply exactly this plan: devx session instances --apply %s%s\n", p.Hash, confirm)
	} else if p.HasWork() {
		_, _ = fmt.Fprintln(out, "\nNot applicable: fix the --confirm list.")
	} else {
		_, _ = fmt.Fprintln(out, "\nNothing to apply.")
	}
	return nil
}

// backupInstanceRecords copies sessions.json and every agent record the plan
// touches into a fresh 0700 directory before anything is written.
func backupInstanceRecords(p *piagent.InstancePlan) (string, error) {
	base := filepath.Join(filepath.Dir(config.GetSessionsPath()), "backups")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	// Unique per run, so concurrent or rapid applies never share a journal.
	dir, err := os.MkdirTemp(base, "session-instances-"+time.Now().UTC().Format("20060102T150405Z")+"-"+p.Hash+"-")
	if err != nil {
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
		if a.Action != piagent.ActionBindAgent && a.Action != piagent.ActionRestoreBinding {
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

// newerApplies lists backup dirs of applies made after dir (by journal
// modification time). Rolling back an older run could remove ids a newer run
// wrote with identical (deterministic) values, so that needs --force-older.
func newerApplies(dir string) ([]string, error) {
	me, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(filepath.Clean(dir))
	matches, _ := filepath.Glob(filepath.Join(base, "session-instances-*", "journal.jsonl"))
	var out []string
	for _, m := range matches {
		if filepath.Dir(m) == filepath.Clean(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(m), "rolled-back")); err == nil {
			continue // already undone
		}
		if st, err := os.Stat(m); err == nil && st.Size() > 0 && st.ModTime().After(me.ModTime()) {
			out = append(out, filepath.Dir(m))
		}
	}
	return out, nil
}

func runInstancesRollback(out io.Writer, dir string) error {
	if newer, err := newerApplies(dir); err != nil {
		return err
	} else if len(newer) > 0 && !instancesForceOlder {
		return fmt.Errorf("a later migration run wrote records after this one (%s); roll back the newest run first, or pass --force-older", strings.Join(newer, ", "))
	}
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
	_ = os.WriteFile(filepath.Join(dir, "rolled-back"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	_, _ = fmt.Fprintf(out, "Rolled back %d change(s) from %s.\n", n, dir)
	for _, s := range skipped {
		_, _ = fmt.Fprintln(out, "  left unchanged: "+s)
	}
	return nil
}
