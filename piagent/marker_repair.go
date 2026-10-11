package piagent

// Backup-based repair of lost local-only ownership markers.
//
// Background: an MCP-created (non-adopted) agent may only run in a session
// whose record carries a local-only marker naming it (EnsureTmux). A DevX
// writer whose Session struct predates the marker drops it from every record
// on any sessions.json write. The agent is then denied relaunch, although
// its ownership is still witnessed elsewhere. Which writer dropped a given
// marker is generally not provable from the records; this repair does not
// depend on knowing it.
//
// The repair restores the EXACT marker a reviewed backup of sessions.json
// holds for the EXACT session record, and only when every independent
// witness agrees and nothing contradicts it:
//
//   - agent: created_by=mcp, not adopted, not retired, the only live
//     claimant of the session and of its worktree;
//   - session: same name/path/project as the agent, not a container, carries
//     NO marker of any kind now, created at most MarkerRepairMaxCreationGap
//     before the agent (never after), and is the instance the agent is bound
//     to if it is bound;
//   - the agent's own agent_created event names the session (and its
//     instance id, if it recorded one);
//   - the live tmux session of that name carries DEVX_LOCAL_ONLY_AGENT equal
//     to the agent id (set atomically by the local-only creator);
//   - every reviewed backup that has this record (same created_at and path)
//     holds the same local-only marker naming the agent, with the record's
//     own created_at (the creator writes both from one clock reading); at
//     least one does, and no backup holds a different marker for it.
//
// Planning is pure and read-only. A candidate is repaired only when the
// owner lists it with --confirm, and the plan (including backup hashes) is
// hashed: apply refuses any other plan. Apply re-validates under the agent
// lock and the sessions lock, journals (fsync) before each write, writes
// only the local_only member of that one record, and is idempotent. Rollback
// removes only a marker that is still exactly the one it wrote.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/jfox85/devx/session"
)

// MarkerRepairMaxCreationGap bounds how long after its session record an
// MCP-created agent may have been created. Start writes the record and then
// the agent within milliseconds; a resumed start can take longer.
const MarkerRepairMaxCreationGap = 2 * time.Minute

// Marker repair plan actions.
const (
	MarkerActionRestore   = "restore_local_only_marker"
	MarkerActionCandidate = "candidate_needs_owner_confirmation"
	MarkerActionSkip      = "skip"
	MarkerActionPresent   = "marker_present"
)

// Reasons a marker is not restored.
const (
	MarkerSkipNotMCP           = "agent was not created by MCP start"
	MarkerSkipAdopted          = "adopted agent: its session is a normal session, never local-only"
	MarkerSkipRetired          = "retired agent"
	MarkerSkipNoSession        = "agent's session no longer exists"
	MarkerSkipOtherMarker      = "session carries a marker that does not name this agent"
	MarkerSkipPath             = "session path differs from the agent's worktree"
	MarkerSkipProject          = "session project differs from the agent's"
	MarkerSkipContainer        = "container session"
	MarkerSkipClaimants        = "another live agent claims the same session"
	MarkerSkipSharedWorktree   = "another live agent or session uses the same worktree"
	MarkerSkipNoTimes          = "session or agent has no creation time"
	MarkerSkipOrdering         = "session record is newer than the agent (recreated)"
	MarkerSkipGap              = "session record is too much older than the agent to be its start"
	MarkerSkipInstance         = "session is not the instance the agent is bound to"
	MarkerSkipForeignID        = "session has an instance id the unbound agent never recorded (recreated)"
	MarkerSkipNoEvent          = "agent's own agent_created event is missing or names a different session/instance"
	MarkerSkipTmuxMissing      = "no live tmux session of this name, so its creation tag cannot be checked"
	MarkerSkipTmuxMismatch     = "live tmux session is not tagged DEVX_LOCAL_ONLY_AGENT=<this agent>"
	MarkerSkipNoBackupWitness  = "no reviewed backup holds a local-only marker for this exact record"
	MarkerSkipBackupConflict   = "reviewed backups disagree about this record's marker"
	MarkerSkipBackupInstanceID = "backup record carries a different instance id than the current record"
	MarkerSkipWitnessError     = "a witness could not be read"
)

// MarkerBackup is one reviewed sessions.json backup.
type MarkerBackup struct {
	Path     string
	SHA256   string
	Sessions map[string]*session.Session
}

// MarkerBackupRef identifies a reviewed backup in the plan.
type MarkerBackupRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// CreationEvent is what an agent's own agent_created event recorded.
type CreationEvent struct {
	Seq               int64  `json:"seq"`
	Session           string `json:"session"`
	SessionInstanceID string `json:"session_instance_id,omitempty"`
}

// MarkerWitnesses are the read-only lookups the planner needs.
type MarkerWitnesses struct {
	// Creation returns the agent's agent_created event (ok=false if none).
	Creation func(agentID string) (CreationEvent, bool, error)
	// TmuxTag returns the DEVX_LOCAL_ONLY_AGENT value of the live tmux
	// session with exactly this name; present=false if no such session.
	TmuxTag func(sessionName string) (value string, present bool, err error)
}

// MarkerPlan is a reviewed, hashable set of marker restorations.
type MarkerPlan struct {
	Version              int               `json:"version"`
	Backups              []MarkerBackupRef `json:"backups"`
	Items                []MarkerPlanItem  `json:"items"`
	OwnerConfirmed       []string          `json:"owner_confirmed,omitempty"`
	UnknownConfirmations []string          `json:"unknown_confirmations,omitempty"`
	Summary              map[string]int    `json:"summary"`
	Notes                []string          `json:"notes,omitempty"`
	Hash                 string            `json:"plan_hash"`
}

// MarkerPlanItem is one MCP-created agent and its session.
type MarkerPlanItem struct {
	AgentID string `json:"agent_id"`
	Session string `json:"session"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
	// Identity of the exact record (apply refuses any other).
	SessionCreatedAt  time.Time `json:"session_created_at,omitempty"`
	SessionPath       string    `json:"session_path,omitempty"`
	SessionInstanceID string    `json:"session_instance_id,omitempty"`
	// Identity of the exact agent.
	AgentCreatedAt time.Time `json:"agent_created_at,omitempty"`
	AgentBoundID   string    `json:"agent_session_instance_id,omitempty"`
	// Evidence.
	SessionVsAgent string   `json:"session_created_vs_agent_created,omitempty"`
	CreationSeq    int64    `json:"agent_created_event_seq,omitempty"`
	TmuxTag        string   `json:"tmux_tag,omitempty"`
	Witnesses      []string `json:"backup_witnesses,omitempty"`
	// Marker is the exact marker that will be written (from the backups).
	Marker *session.LocalOnlyMeta `json:"marker,omitempty"`
}

// MarkerPlanOptions configures PlanMarkerRepair.
type MarkerPlanOptions struct {
	Backups   []MarkerBackup
	Witnesses MarkerWitnesses
	Confirmed []string
}

// PlanMarkerRepair computes the repair plan. It reads only.
func PlanMarkerRepair(sessions map[string]*session.Session, agents []*Agent, opts MarkerPlanOptions) *MarkerPlan {
	p := &MarkerPlan{Version: 1, Summary: map[string]int{}}
	backups := append([]MarkerBackup(nil), opts.Backups...)
	sort.Slice(backups, func(i, j int) bool { return backups[i].Path < backups[j].Path })
	for _, b := range backups {
		p.Backups = append(p.Backups, MarkerBackupRef{Path: b.Path, SHA256: b.SHA256})
	}
	confirmed := map[string]bool{}
	var confirmList []string
	for _, id := range opts.Confirmed {
		if !confirmed[id] {
			confirmed[id] = true
			confirmList = append(confirmList, id)
		}
	}
	used := map[string]bool{}

	live := map[string]int{}
	liveByPath := map[string]int{}
	for _, a := range agents {
		if a == nil || a.RetiredAt != nil {
			continue
		}
		live[a.DevxSession]++
		liveByPath[filepath.Clean(a.Worktree)]++
	}
	sessByPath := map[string]int{}
	for _, s := range sessions {
		if s != nil && s.Path != "" {
			sessByPath[filepath.Clean(s.Path)]++
		}
	}

	sorted := make([]*Agent, 0, len(agents))
	for _, a := range agents {
		if a != nil {
			sorted = append(sorted, a)
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, a := range sorted {
		if a.CreatedBy != "mcp" {
			continue // only MCP-started agents ever had a local-only marker
		}
		it := MarkerPlanItem{AgentID: a.ID, Session: a.DevxSession, AgentCreatedAt: a.CreatedAt, AgentBoundID: a.SessionInstanceID}
		reason := markerEligibility(a, sessions[a.DevxSession], live, liveByPath, sessByPath, &it, backups, opts.Witnesses)
		switch {
		case reason == MarkerActionPresent && !confirmed[a.ID]:
			it.Action = MarkerActionPresent
		case reason != "" && reason != MarkerActionPresent:
			it.Action, it.Reason, it.Marker = MarkerActionSkip, reason, nil
		case reason == MarkerActionPresent && confirmed[a.ID]:
			// Restored by an earlier (possibly interrupted) apply of the
			// same confirmation set: the confirmation is satisfied, so
			// re-running the same command plans only what remains.
			used[a.ID] = true
			it.Action = MarkerActionPresent
		case confirmed[a.ID]:
			used[a.ID] = true
			it.Action = MarkerActionRestore
		default:
			it.Action = MarkerActionCandidate
			it.Reason = "all witnesses agree; restore only after owner confirmation: --confirm " + a.ID
		}
		p.Items = append(p.Items, it)
	}
	for _, id := range confirmList {
		if used[id] {
			p.OwnerConfirmed = append(p.OwnerConfirmed, id)
		} else {
			p.UnknownConfirmations = append(p.UnknownConfirmations, id)
		}
	}
	sort.Strings(p.OwnerConfirmed)
	sort.Strings(p.UnknownConfirmations)
	for _, it := range p.Items {
		p.Summary[it.Action]++
	}
	p.Notes = []string{
		"Only session.local_only of each confirmed record is written, with the exact marker from the reviewed backups.",
		"No agent binding, instance id, lease, queue, tmux session, pane or process is changed.",
		"Skipped agents are left unchanged: their ownership is not proven by independent witnesses.",
	}
	p.Hash = p.computeHash()
	return p
}

// markerEligibility fills the evidence on it and returns "" if the marker
// may be restored, MarkerActionPresent if the record already has exactly
// this agent's marker, or a skip reason.
func markerEligibility(a *Agent, s *session.Session, live, liveByPath, sessByPath map[string]int, it *MarkerPlanItem, backups []MarkerBackup, w MarkerWitnesses) string {
	switch {
	case a.Adopted:
		return MarkerSkipAdopted
	case a.RetiredAt != nil:
		return MarkerSkipRetired
	case s == nil:
		return MarkerSkipNoSession
	}
	it.SessionCreatedAt, it.SessionPath, it.SessionInstanceID = s.CreatedAt, s.Path, s.InstanceID
	if !s.CreatedAt.IsZero() && !a.CreatedAt.IsZero() {
		it.SessionVsAgent = s.CreatedAt.Sub(a.CreatedAt).Round(time.Millisecond).String()
	}
	if s.LocalOnly != nil && s.ManagedAgent == "" && s.LocalOnly.AgentID == a.ID && s.LocalOnly.Owner == session.LocalOnlyOwnerPiMCP {
		return MarkerActionPresent
	}
	wt := filepath.Clean(a.Worktree)
	switch {
	case s.LocalOnly != nil || s.ManagedAgent != "":
		return MarkerSkipOtherMarker
	case s.Path == "" || !filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != wt:
		return MarkerSkipPath
	case a.Project == "" || s.ProjectAlias != a.Project:
		return MarkerSkipProject
	case s.IsContainerized():
		return MarkerSkipContainer
	case live[a.DevxSession] != 1:
		return MarkerSkipClaimants
	case liveByPath[wt] != 1 || sessByPath[wt] != 1:
		return MarkerSkipSharedWorktree
	case s.CreatedAt.IsZero() || a.CreatedAt.IsZero():
		return MarkerSkipNoTimes
	case s.CreatedAt.After(a.CreatedAt):
		return MarkerSkipOrdering
	case a.CreatedAt.Sub(s.CreatedAt) > MarkerRepairMaxCreationGap:
		return MarkerSkipGap
	case a.SessionInstanceID != "" && !session.MatchesBoundInstance(s, a.SessionInstanceID, a.SessionCreatedAt):
		return MarkerSkipInstance
	}
	// Agent's own creation event.
	if w.Creation == nil {
		return MarkerSkipNoEvent
	}
	ev, ok, err := w.Creation(a.ID)
	if err != nil {
		return MarkerSkipWitnessError
	}
	if !ok || ev.Session != a.DevxSession ||
		(ev.SessionInstanceID != "" && ev.SessionInstanceID != a.SessionInstanceID) {
		return MarkerSkipNoEvent
	}
	if s.InstanceID != "" && s.InstanceID != ev.SessionInstanceID && s.InstanceID != a.SessionInstanceID {
		// The record's id is not one the agent ever recorded: the
		// migration may have assigned one (derived), anything else means
		// the record was recreated after the agent.
		if s.InstanceID != session.DeriveInstanceID(a.DevxSession, s.Path, s.CreatedAt) {
			return MarkerSkipForeignID
		}
	}
	it.CreationSeq = ev.Seq
	// Live tmux tag.
	if w.TmuxTag == nil {
		return MarkerSkipTmuxMissing
	}
	tag, present, err := w.TmuxTag(a.DevxSession)
	if err != nil {
		return MarkerSkipWitnessError
	}
	if !present {
		return MarkerSkipTmuxMissing
	}
	it.TmuxTag = tag
	if tag != a.ID {
		return MarkerSkipTmuxMismatch
	}
	// Backups.
	var marker *session.LocalOnlyMeta
	for _, b := range backups {
		bs := b.Sessions[a.DevxSession]
		if bs == nil || !bs.CreatedAt.Equal(s.CreatedAt) || filepath.Clean(bs.Path) != filepath.Clean(s.Path) {
			continue // not this record
		}
		if bs.InstanceID != "" && s.InstanceID != "" && bs.InstanceID != s.InstanceID {
			return MarkerSkipBackupInstanceID
		}
		if bs.ManagedAgent != "" {
			return MarkerSkipBackupConflict
		}
		if bs.LocalOnly == nil {
			continue // a backup taken after the loss: not a witness
		}
		m := *bs.LocalOnly
		if m.Owner != session.LocalOnlyOwnerPiMCP || m.AgentID != a.ID || !m.CreatedAt.Equal(bs.CreatedAt) {
			return MarkerSkipBackupConflict
		}
		if marker != nil && !sameMarker(marker, &m) {
			return MarkerSkipBackupConflict
		}
		marker = &m
		it.Witnesses = append(it.Witnesses, b.Path)
	}
	if marker == nil {
		return MarkerSkipNoBackupWitness
	}
	it.Marker = marker
	return ""
}

func (p *MarkerPlan) computeHash() string {
	cp := *p
	cp.Hash = ""
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:16]
}

// VerifyHash reports whether the plan content still matches its hash.
func (p *MarkerPlan) VerifyHash() bool { return p.Hash != "" && p.Hash == p.computeHash() }

// HasWork reports whether applying the plan would write anything.
func (p *MarkerPlan) HasWork() bool {
	for _, it := range p.Items {
		if it.Action == MarkerActionRestore {
			return true
		}
	}
	return false
}

// MarkerJournalEntry is one applied marker write (newline-delimited JSON).
type MarkerJournalEntry struct {
	Time              time.Time             `json:"time"`
	Kind              string                `json:"kind"` // "local_only_marker"
	Session           string                `json:"session"`
	SessionCreatedAt  time.Time             `json:"session_created_at"`
	SessionPath       string                `json:"session_path"`
	SessionInstanceID string                `json:"session_instance_id,omitempty"`
	AgentID           string                `json:"agent_id"`
	Marker            session.LocalOnlyMeta `json:"marker"`
	PlanHash          string                `json:"plan_hash"`
}

// MarkerStores is the narrow persistence surface the repair needs.
type MarkerStores struct {
	// MutateSessions applies fn to the latest records under the sessions
	// lock and persists them atomically (no write if fn returns an error).
	MutateSessions func(fn func(map[string]*session.Session) error) error
	Agents         *Store
	// VerifyBackup re-reads a reviewed backup and returns its sha256.
	VerifyBackup func(path string) (string, error)
	// TmuxTag re-checks the live tmux tag at apply time.
	TmuxTag func(sessionName string) (string, bool, error)
	// Journal appends one entry durably (fsync) before returning.
	Journal func(MarkerJournalEntry) error
	Now     func() time.Time
}

// ApplyMarkerPlan writes the confirmed restorations of a reviewed plan.
func ApplyMarkerPlan(p *MarkerPlan, st MarkerStores) error {
	if !p.VerifyHash() {
		return fmt.Errorf("plan content does not match its hash")
	}
	if len(p.UnknownConfirmations) > 0 {
		return fmt.Errorf("plan names agents that are not candidates: %w", ErrPlanStale)
	}
	now := st.Now
	if now == nil {
		now = time.Now
	}
	if st.VerifyBackup == nil || st.TmuxTag == nil || st.Journal == nil {
		return errors.New("marker repair needs backup, tmux and journal checks")
	}
	for _, b := range p.Backups {
		sum, err := st.VerifyBackup(b.Path)
		if err != nil {
			return fmt.Errorf("backup %s: %w", b.Path, err)
		}
		if sum != b.SHA256 {
			return fmt.Errorf("backup %s changed since review: %w", b.Path, ErrPlanStale)
		}
	}
	for _, it := range p.Items {
		if it.Action != MarkerActionRestore {
			continue
		}
		if err := restoreMarkerChecked(p.Hash, it, st, now); err != nil {
			return err
		}
	}
	return nil
}

func restoreMarkerChecked(planHash string, it MarkerPlanItem, st MarkerStores, now func() time.Time) error {
	if it.Marker == nil || it.Marker.AgentID != it.AgentID || it.Marker.Owner != session.LocalOnlyOwnerPiMCP {
		return fmt.Errorf("agent %s: plan item has no valid marker: %w", it.AgentID, ErrPlanStale)
	}
	return st.Agents.WithAgentLock(it.AgentID, func() error {
		a, err := st.Agents.LoadAgent(it.AgentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", it.AgentID, err)
		}
		if a.CreatedBy != "mcp" || a.Adopted || a.RetiredAt != nil || a.DevxSession != it.Session ||
			!a.CreatedAt.Equal(it.AgentCreatedAt) || a.SessionInstanceID != it.AgentBoundID ||
			filepath.Clean(a.Worktree) != filepath.Clean(it.SessionPath) {
			return fmt.Errorf("agent %s changed since review: %w", it.AgentID, ErrPlanStale)
		}
		tag, present, err := st.TmuxTag(it.Session)
		if err != nil {
			return fmt.Errorf("tmux session %q: %w", it.Session, err)
		}
		if !present || tag != it.AgentID {
			return fmt.Errorf("tmux session %q is no longer tagged for agent %s: %w", it.Session, it.AgentID, ErrPlanStale)
		}
		wrote := false
		err = st.MutateSessions(func(sessions map[string]*session.Session) error {
			s := sessions[it.Session]
			if s == nil || !s.CreatedAt.Equal(it.SessionCreatedAt) || filepath.Clean(s.Path) != filepath.Clean(it.SessionPath) ||
				s.InstanceID != it.SessionInstanceID || s.ProjectAlias != a.Project || s.IsContainerized() {
				return fmt.Errorf("session %q is not the reviewed record: %w", it.Session, ErrPlanStale)
			}
			if s.ManagedAgent != "" {
				return fmt.Errorf("session %q gained an adoption marker: %w", it.Session, ErrPlanStale)
			}
			if s.LocalOnly != nil {
				if sameMarker(s.LocalOnly, it.Marker) {
					return errAlreadyApplied // idempotent: an earlier (interrupted or concurrent) apply wrote it
				}
				return fmt.Errorf("session %q gained a different marker: %w", it.Session, ErrPlanStale)
			}
			for name, o := range sessions {
				if name != it.Session && o != nil && o.LocalOnly != nil && o.LocalOnly.AgentID == it.AgentID {
					return fmt.Errorf("session %q already carries a marker for agent %s: %w", name, it.AgentID, ErrPlanStale)
				}
			}
			if err := st.Journal(MarkerJournalEntry{Time: now(), Kind: "local_only_marker", Session: it.Session,
				SessionCreatedAt: s.CreatedAt, SessionPath: s.Path, SessionInstanceID: s.InstanceID,
				AgentID: it.AgentID, Marker: *it.Marker, PlanHash: planHash}); err != nil {
				return err
			}
			m := *it.Marker
			s.LocalOnly = &m
			wrote = true
			return nil
		})
		if errors.Is(err, errAlreadyApplied) {
			return nil
		}
		if err != nil || !wrote {
			return err
		}
		// Best-effort notification. The authoritative record of the write
		// is the journal entry, fsynced before the sessions write; a crash
		// between the write and this append leaves the journal only.
		_, err = st.Agents.AppendEvent(a.ID, Event{Type: "local_only_marker_restored", Source: "devx", Data: map[string]any{
			"session": it.Session, "plan_hash": planHash, "witnesses": it.Witnesses, "via": "devx session markers"}})
		return err
	})
}

// JournalMatchesPlan reports whether journal entry e records exactly one of
// the plan's confirmed restorations (same agent, session record identity and
// marker). Rollback acts only on such entries.
func JournalMatchesPlan(e MarkerJournalEntry, p *MarkerPlan) bool {
	if e.Kind != "local_only_marker" || e.PlanHash != p.Hash {
		return false
	}
	for _, it := range p.Items {
		if it.Action == MarkerActionRestore && it.AgentID == e.AgentID && it.Session == e.Session &&
			it.SessionCreatedAt.Equal(e.SessionCreatedAt) && filepath.Clean(it.SessionPath) == filepath.Clean(e.SessionPath) &&
			it.SessionInstanceID == e.SessionInstanceID && sameMarker(it.Marker, &e.Marker) {
			return true
		}
	}
	return false
}

var errAlreadyApplied = errors.New("already applied")

// sameMarker compares two markers exactly, including members this build
// does not know (preserved in Unknown).
func sameMarker(a, b *session.LocalOnlyMeta) bool {
	if a == nil || b == nil || a.Owner != b.Owner || a.AgentID != b.AgentID || !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	if len(a.Unknown) != len(b.Unknown) {
		return false
	}
	for k, va := range a.Unknown {
		vb, ok := b.Unknown[k]
		if !ok || !jsonEqual(va, vb) {
			return false
		}
	}
	return true
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}

// RollbackMarkers removes exactly the markers recorded in the journal, and
// only where the record is still the same instance holding that same marker.
func RollbackMarkers(entries []MarkerJournalEntry, st MarkerStores) (reverted int, skipped []string, err error) {
	var undone []MarkerJournalEntry
	err = st.MutateSessions(func(sessions map[string]*session.Session) error {
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.Kind != "local_only_marker" {
				continue
			}
			s := sessions[e.Session]
			switch {
			case s == nil:
				skipped = append(skipped, "session "+e.Session+": removed since repair")
			case !s.CreatedAt.Equal(e.SessionCreatedAt) || filepath.Clean(s.Path) != filepath.Clean(e.SessionPath) ||
				(e.SessionInstanceID != "" && s.InstanceID != "" && s.InstanceID != e.SessionInstanceID):
				skipped = append(skipped, "session "+e.Session+": not the repaired record (recreated or changed)")
			case s.LocalOnly == nil:
				skipped = append(skipped, "session "+e.Session+": no marker (repair write did not happen or was already undone)")
			case !sameMarker(s.LocalOnly, &e.Marker):
				skipped = append(skipped, "session "+e.Session+": marker changed since repair")
			default:
				s.LocalOnly = nil
				reverted++
				undone = append(undone, e)
			}
		}
		return nil
	})
	if err != nil {
		return 0, skipped, err
	}
	for _, e := range undone {
		_ = st.Agents.WithAgentLock(e.AgentID, func() error {
			_, err := st.Agents.AppendEvent(e.AgentID, Event{Type: "local_only_marker_rollback", Source: "devx", Data: map[string]any{
				"session": e.Session, "plan_hash": e.PlanHash, "via": "devx session markers --rollback"}})
			return err
		})
	}
	return reverted, skipped, nil
}

// CreationEventOf returns the agent's first agent_created event.
func (s *Store) CreationEventOf(agentID string) (CreationEvent, bool, error) {
	after := int64(0)
	for {
		evs, last, err := s.ReadEvents(agentID, after, MaxEventsPage)
		if err != nil {
			return CreationEvent{}, false, err
		}
		for _, e := range evs {
			if e.Type != "agent_created" {
				continue
			}
			ce := CreationEvent{Seq: e.Seq}
			ce.Session, _ = e.Data["session"].(string)
			ce.SessionInstanceID, _ = e.Data["session_instance_id"].(string)
			return ce, true, nil
		}
		if len(evs) == 0 {
			return CreationEvent{}, false, nil
		}
		after = evs[len(evs)-1].Seq
		if after >= last {
			return CreationEvent{}, false, nil
		}
	}
}
