package cmd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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
	markersBackups  []string
	markersConfirm  []string
	markersApply    string
	markersRollback string
	markersJSON     bool
	markersForce    bool
)

var sessionMarkersCmd = &cobra.Command{
	Use:   "markers",
	Short: "Review restoring lost local-only ownership markers from backups (dry run by default)",
	Long: `Restore the local-only ownership marker of MCP-created agent sessions whose
marker an older DevX writer dropped from sessions.json, using reviewed
backups of sessions.json as the source of the exact marker.

Without --apply/--rollback this is a READ-ONLY DRY RUN: it reads the records,
the backups, each agent's own event log and the live tmux session tags, and
writes nothing.

  devx session markers --backup <file> ...                              # review
  devx session markers --backup <file> ... --confirm <agent-id> ...     # plan that restores confirmed agents
  devx session markers --backup <file> ... --confirm ... --apply <hash> # apply exactly that plan
  devx session markers --rollback <dir>                                 # undo a previous apply

A marker is proposed only when every witness agrees: the agent was created
by MCP start, is not adopted or retired, and is the only live claimant of the
session and its worktree; the session has no marker now, matches the agent's
path and project, was created shortly before the agent and is the instance it
is bound to (if bound); the agent's own agent_created event names it; the
live tmux session is tagged DEVX_LOCAL_ONLY_AGENT=<agent-id>; and the
reviewed backups hold exactly one local-only marker for this exact record.
Anything ambiguous is listed with a reason and left unchanged. Even then a
marker is restored only for agents the owner lists with --confirm.

Backups must be regular files owned by you and not writable by others; their
SHA-256 is part of the plan hash and re-checked at apply time. --apply copies
sessions.json to a fresh backup directory first, re-validates every
condition under the agent and sessions locks, journals before writing, and
changes only session.local_only. It never touches agent bindings, instance
ids, leases, queues, tmux sessions, panes or processes. After an interrupted
apply, re-run the same dry run (same --confirm list) and apply its new hash.

--rollback accepts only a session-markers-* run directory created by
--apply, newest run first (or --force-older), and removes a marker only where
the record and marker are still exactly as written.

Every witness can be written by processes running as you; the owner's
--confirm after reviewing the dry run is the authorization. --apply and
--rollback refuse to run inside a managed agent (a guardrail, not a boundary).`,
	Args: cobra.NoArgs,
	RunE: runSessionMarkers,
}

func init() {
	sessionCmd.AddCommand(sessionMarkersCmd)
	sessionMarkersCmd.Flags().StringArrayVar(&markersBackups, "backup", nil, "reviewed sessions.json backup to take markers from (repeatable)")
	sessionMarkersCmd.Flags().StringSliceVar(&markersConfirm, "confirm", nil, "owner-confirmed candidate agent ids to restore; part of the plan hash")
	sessionMarkersCmd.Flags().StringVar(&markersApply, "apply", "", "apply the plan with this hash (from a dry run)")
	sessionMarkersCmd.Flags().StringVar(&markersRollback, "rollback", "", "undo the marker repair recorded in this backup directory")
	sessionMarkersCmd.Flags().BoolVar(&markersJSON, "json", false, "print the plan as JSON")
	sessionMarkersCmd.Flags().BoolVar(&markersForce, "force-older", false, "allow --rollback of a run that is not the newest")
}

// errInsideAgent: ownership repairs are owner actions.
var errInsideAgent = errors.New("refusing to change ownership markers from inside a managed agent (DEVX_PI_AGENT_ID is set); run this yourself in a normal shell")

func runSessionMarkers(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	if markersRollback != "" {
		if os.Getenv("DEVX_PI_AGENT_ID") != "" {
			return errInsideAgent
		}
		return runMarkersRollback(out, markersRollback)
	}
	if len(markersBackups) == 0 {
		return errors.New("at least one --backup <sessions.json backup> is required")
	}
	plan, err := currentMarkerPlan()
	if err != nil {
		return err
	}
	if markersApply == "" {
		return printMarkerPlan(out, plan)
	}
	if os.Getenv("DEVX_PI_AGENT_ID") != "" {
		return errInsideAgent
	}
	if len(plan.UnknownConfirmations) > 0 {
		return fmt.Errorf("--confirm names agents that are not candidates in this plan: %s; re-run the dry run", strings.Join(plan.UnknownConfirmations, ", "))
	}
	if markersApply != plan.Hash {
		return fmt.Errorf("plan hash %s does not match the current plan %s: records, backups, tags or confirmations changed; re-run the dry run", markersApply, plan.Hash)
	}
	if !plan.HasWork() {
		_, _ = fmt.Fprintln(out, "Nothing to apply.")
		return nil
	}
	dir, err := backupForMarkerRepair(plan)
	if err != nil {
		return fmt.Errorf("backup failed, nothing written: %w", err)
	}
	jf, err := os.OpenFile(filepath.Join(dir, "journal.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = jf.Close() }()
	st := markerStores(func(e piagent.MarkerJournalEntry) error {
		b, _ := json.Marshal(e)
		if _, err := jf.Write(append(b, '\n')); err != nil {
			return err
		}
		return jf.Sync()
	})
	if err := piagent.ApplyMarkerPlan(plan, st); err != nil {
		return fmt.Errorf("apply stopped (backup and journal in %s; re-run the dry run to see what remains, or --rollback %s): %w", dir, dir, err)
	}
	_, _ = fmt.Fprintf(out, "Applied marker plan %s. Backup and journal: %s\nUndo with: devx session markers --rollback %s\n", plan.Hash, dir, dir)
	return nil
}

func markerStores(journal func(piagent.MarkerJournalEntry) error) piagent.MarkerStores {
	return piagent.MarkerStores{
		MutateSessions: func(fn func(map[string]*session.Session) error) error {
			return (&session.SessionStore{}).Mutate(func(fresh *session.SessionStore) error { return fn(fresh.Sessions) })
		},
		Agents: piagent.NewStore(piAgentStateDir()),
		VerifyBackup: func(path string) (string, error) {
			b, err := readTrustedBackup(path)
			if err != nil {
				return "", err
			}
			return b.SHA256, nil
		},
		TmuxTag: session.LocalOnlyTmuxTag,
		Journal: journal,
	}
}

func currentMarkerPlan() (*piagent.MarkerPlan, error) {
	st, err := session.LoadSessions()
	if err != nil {
		return nil, err
	}
	store := piagent.NewStore(piAgentStateDir())
	agents, err := store.ListAgents()
	if err != nil {
		return nil, err
	}
	var backups []piagent.MarkerBackup
	seen := map[string]bool{}
	for _, p := range markersBackups {
		b, err := readTrustedBackup(p)
		if err != nil {
			return nil, err
		}
		if seen[b.Path] {
			continue
		}
		seen[b.Path] = true
		backups = append(backups, b)
	}
	return piagent.PlanMarkerRepair(st.Sessions, agents, piagent.MarkerPlanOptions{
		Backups:   backups,
		Witnesses: piagent.MarkerWitnesses{Creation: store.CreationEventOf, TmuxTag: session.LocalOnlyTmuxTag},
		Confirmed: markersConfirm,
	}), nil
}

// readTrustedBackup reads a sessions.json backup the owner named. It must be
// a regular file (not a symlink) owned by the current user and not writable
// by group or others, and must not be the live sessions.json. The checks run
// on the opened file and the bytes are read from that same handle, so the
// file cannot be swapped between check and read.
func readTrustedBackup(path string) (piagent.MarkerBackup, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return piagent.MarkerBackup{}, err
	}
	if live, err := filepath.Abs(config.GetSessionsPath()); err == nil && samePath(abs, live) {
		return piagent.MarkerBackup{}, fmt.Errorf("backup %s is the live sessions.json", path)
	}
	data, err := readTrustedFile(abs)
	if err != nil {
		return piagent.MarkerBackup{}, fmt.Errorf("backup %s: %w", path, err)
	}
	var st session.SessionStore
	if err := json.Unmarshal(data, &st); err != nil {
		return piagent.MarkerBackup{}, fmt.Errorf("backup %s is not a sessions.json: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return piagent.MarkerBackup{Path: abs, SHA256: hex.EncodeToString(sum[:]), Sessions: st.Sessions}, nil
}

// readTrustedFile opens path without following a final symlink, checks the
// opened file is a regular file owned by the current user and not writable
// by group or others, and returns its contents read from the same handle.
func readTrustedFile(path string) ([]byte, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("writable by group or others")
	}
	if err := checkOwnedByMe(fi); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func printMarkerPlan(out io.Writer, p *piagent.MarkerPlan) error {
	if markersJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}
	_, _ = fmt.Fprintf(out, "DRY RUN - read-only, nothing written. Plan hash: %s\n", p.Hash)
	for _, b := range p.Backups {
		_, _ = fmt.Fprintf(out, "Backup: %s sha256=%s\n", b.Path, b.SHA256)
	}
	if len(p.UnknownConfirmations) > 0 {
		_, _ = fmt.Fprintf(out, "NOT CANDIDATES (cannot be confirmed; plan not applicable): %s\n", strings.Join(p.UnknownConfirmations, ", "))
	}
	_, _ = fmt.Fprintln(out)
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "AGENT\tSESSION\tACTION\tSESSION-AGENT CREATED\tTMUX TAG\tWITNESSES\tREASON")
	for _, it := range p.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", it.AgentID, it.Session, it.Action, it.SessionVsAgent, it.TmuxTag, len(it.Witnesses), it.Reason)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "\nSummary: %v\n", p.Summary)
	for _, n := range p.Notes {
		_, _ = fmt.Fprintln(out, "- "+n)
	}
	switch {
	case p.HasWork() && len(p.UnknownConfirmations) == 0:
		args := ""
		for _, b := range p.Backups {
			args += " --backup " + b.Path
		}
		for _, id := range p.OwnerConfirmed {
			args += " --confirm " + id
		}
		_, _ = fmt.Fprintf(out, "\nTo apply exactly this plan: devx session markers%s --apply %s\n", args, p.Hash)
	case p.HasWork():
		_, _ = fmt.Fprintln(out, "\nNot applicable: fix the --confirm list.")
	default:
		_, _ = fmt.Fprintln(out, "\nNothing to apply.")
	}
	return nil
}

func backupForMarkerRepair(p *piagent.MarkerPlan) (string, error) {
	base := markerRunsBase()
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "session-markers-"+time.Now().UTC().Format("20060102T150405Z")+"-"+p.Hash+"-")
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(config.GetSessionsPath())
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), b, 0o600); err != nil {
		return "", err
	}
	plan, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), plan, 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// markerRunsBase is where apply creates its per-run backup directories.
func markerRunsBase() string {
	return filepath.Join(filepath.Dir(config.GetSessionsPath()), "backups")
}

// newerMarkerRuns lists marker-repair runs that journaled after dir and were
// not rolled back. Two runs can write identical markers, so undoing an older
// run could remove a marker a newer run wrote.
func newerMarkerRuns(dir string) ([]string, error) {
	me, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		return nil, err
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Clean(dir)), "session-markers-*", "journal.jsonl"))
	var out []string
	for _, m := range matches {
		d := filepath.Dir(m)
		if d == filepath.Clean(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "rolled-back")); err == nil {
			continue
		}
		if st, err := os.Stat(m); err == nil && st.Size() > 0 && st.ModTime().After(me.ModTime()) {
			out = append(out, d)
		}
	}
	return out, nil
}

func runMarkersRollback(out io.Writer, dir string) error {
	// Only a run directory this command created is accepted: it lives in
	// the backups directory next to sessions.json, and its journal and plan
	// are trusted files whose entries belong to that plan.
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(abs), markerRunsBase()) || !strings.HasPrefix(filepath.Base(abs), "session-markers-") {
		return fmt.Errorf("%s is not a marker repair run directory under %s", dir, markerRunsBase())
	}
	planData, err := readTrustedFile(filepath.Join(abs, "plan.json"))
	if err != nil {
		return fmt.Errorf("plan.json in %s: %w", dir, err)
	}
	var plan piagent.MarkerPlan
	if err := json.Unmarshal(planData, &plan); err != nil || !plan.VerifyHash() {
		return fmt.Errorf("plan.json in %s is not a valid marker plan", dir)
	}
	journalData, err := readTrustedFile(filepath.Join(abs, "journal.jsonl"))
	if err != nil {
		return fmt.Errorf("journal in %s: %w", dir, err)
	}
	if newer, err := newerMarkerRuns(abs); err != nil {
		return err
	} else if len(newer) > 0 && !markersForce {
		return fmt.Errorf("a later marker repair run wrote after this one (%s); roll back the newest run first, or pass --force-older", strings.Join(newer, ", "))
	}
	var entries []piagent.MarkerJournalEntry
	sc := bufio.NewScanner(strings.NewReader(string(journalData)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e piagent.MarkerJournalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			break // torn last line: that write never happened (journal precedes it)
		}
		if e.PlanHash != plan.Hash {
			return fmt.Errorf("journal entry for %s belongs to plan %s, not this run's plan %s", e.Session, e.PlanHash, plan.Hash)
		}
		entries = append(entries, e)
	}
	n, skipped, err := piagent.RollbackMarkers(entries, markerStores(nil))
	if err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, "rolled-back"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	_, _ = fmt.Fprintf(out, "Rolled back %d marker(s) from %s.\n", n, dir)
	for _, s := range skipped {
		_, _ = fmt.Fprintln(out, "  left unchanged: "+s)
	}
	return nil
}
