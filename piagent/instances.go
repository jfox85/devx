package piagent

// Session-instance migration for records from before instance ids.
//
// Plan is pure: it looks at the current session and agent records and
// proposes, for each session without an instance id, a new random id, and
// for each non-retired agent without a binding, a binding to its session's
// (current or proposed) id. It binds an agent ONLY when the evidence
// identifies exactly one session record and nothing contradicts it.
// Anything ambiguous is reported with a reason and left unchanged (fail
// closed); the owner resolves it explicitly (retire the agent, adopt again,
// or fix the record).
//
// ApplyPlan writes exactly the planned fields, after re-validating every
// precondition under the session and agent locks, and journals each step.
// Re-running Plan after a (partial) apply proposes only what is still
// missing, so the migration is repeatable and an interrupted apply is
// completed by planning and applying again. Rollback removes only the fields
// this migration wrote, and only where they still hold the value it wrote.

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

// Plan item actions.
const (
	ActionAssignSessionID = "assign_session_instance_id"
	ActionBindAgent       = "bind_agent"
	ActionSkip            = "skip"
	ActionBound           = "already_bound"
	// ActionRestoreBinding re-writes a binding an older writer dropped from
	// agent.json, from the agent's own event log (never a new binding).
	ActionRestoreBinding = "restore_agent_binding"
)

// Reasons an agent is not bound automatically.
const (
	SkipRetired          = "retired agent: never rebound"
	SkipNoSession        = "agent's session no longer exists"
	SkipPathMismatch     = "session path differs from the agent's worktree"
	SkipProjectMismatch  = "session project differs from the agent's"
	SkipForeignMarker    = "session marker names a different agent"
	SkipBothMarkers      = "session carries both local-only and adoption markers"
	SkipDuplicateClaim   = "another live agent claims the same session"
	SkipSharedWorktree   = "another live agent or session uses the same worktree"
	SkipSessionNewer     = "session record is newer than the agent (likely recreated after the agent was created)"
	SkipNoTimes          = "session or agent has no creation time to compare"
	SkipContainer        = "container session"
	SkipBoundElsewhere   = "agent is bound to a different session instance (session was recreated)"
	SkipBoundIDMissing   = "agent is bound but the session lost its instance id and created_at does not match"
	SkipInvalidSessionID = "session has a malformed instance id"
	SkipSessionHasID     = "session has an instance id this migration did not assign, so it was created after this legacy agent (recreated)"
)

// InstancePlan is a reviewed, hashable set of changes.
type InstancePlan struct {
	Version  int               `json:"version"`
	Sessions []SessionPlanItem `json:"sessions"`
	Agents   []AgentPlanItem   `json:"agents"`
	Summary  map[string]int    `json:"summary"`
	Hash     string            `json:"plan_hash"`
	Notes    []string          `json:"notes,omitempty"`
}

// SessionPlanItem is one session record.
type SessionPlanItem struct {
	Session    string `json:"session"`
	Action     string `json:"action"`
	InstanceID string `json:"instance_id,omitempty"`
	Existing   bool   `json:"existing_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Identity of the exact record the id is written to: apply refuses if
	// the record at this name no longer has this created_at and path.
	CreatedAt time.Time `json:"created_at"`
	Path      string    `json:"path"`
}

// AgentPlanItem is one agent record.
type AgentPlanItem struct {
	AgentID           string `json:"agent_id"`
	Session           string `json:"session"`
	Action            string `json:"action"`
	Reason            string `json:"reason,omitempty"`
	SessionInstanceID string `json:"session_instance_id,omitempty"`
	// SessionCreatedAt is recorded on the agent as the drop-witness.
	SessionCreatedAt time.Time `json:"session_created_at,omitempty"`
	// Evidence for the owner (no secrets, no file contents).
	Marker          string `json:"marker"` // local_only | managed_agent | none
	SessionVsAgent  string `json:"session_created_vs_agent_created,omitempty"`
	Adopted         bool   `json:"adopted,omitempty"`
	Retired         bool   `json:"retired,omitempty"`
	CurrentBindings string `json:"current_binding,omitempty"`
}

// PlanInstances computes the migration plan. sessions and agents are the
// current records; newID proposes the id for a session that has none
// (deterministic in production so a reviewed plan hash stays valid).
func PlanInstances(sessions map[string]*session.Session, agents []*Agent, newID func(name string) string) *InstancePlan {
	return PlanInstancesWithHistory(sessions, agents, newID, nil)
}

// RecordedBindingFunc returns an agent's binding as recorded in its event
// log ("" if none); see Store.RecordedBinding.
type RecordedBindingFunc func(agentID string) (string, time.Time, error)

// PlanInstancesWithHistory is PlanInstances that also detects agents whose
// agent.json lost its binding (an older DevX binary rewrote the record) and
// restores the binding recorded in their event log, instead of treating
// them as unbound legacy agents.
func PlanInstancesWithHistory(sessions map[string]*session.Session, agents []*Agent, newID func(name string) string, recorded RecordedBindingFunc) *InstancePlan {
	p := &InstancePlan{Version: 1, Summary: map[string]int{}}
	names := make([]string, 0, len(sessions))
	for n := range sessions {
		names = append(names, n)
	}
	sort.Strings(names)
	proposed := map[string]string{} // session -> id it will have after apply
	for _, n := range names {
		s := sessions[n]
		if s == nil {
			continue
		}
		switch {
		case s.InstanceID == "":
			id := newID(n)
			proposed[n] = id
			p.Sessions = append(p.Sessions, SessionPlanItem{Session: n, Action: ActionAssignSessionID, InstanceID: id, CreatedAt: s.CreatedAt, Path: s.Path})
		case !session.ValidInstanceID(s.InstanceID):
			p.Sessions = append(p.Sessions, SessionPlanItem{Session: n, Action: ActionSkip, InstanceID: s.InstanceID, Reason: SkipInvalidSessionID})
		default:
			proposed[n] = s.InstanceID
		}
	}

	live := map[string][]string{}   // session -> live agent ids claiming it
	byPath := map[string][]string{} // clean worktree -> live agent ids
	for _, a := range agents {
		if a == nil || a.RetiredAt != nil {
			continue
		}
		live[a.DevxSession] = append(live[a.DevxSession], a.ID)
		byPath[filepath.Clean(a.Worktree)] = append(byPath[filepath.Clean(a.Worktree)], a.ID)
	}
	restoredBy := map[string]string{} // session -> bound id being restored
	sessByPath := map[string][]string{}
	for _, n := range names {
		if s := sessions[n]; s != nil && s.Path != "" {
			sessByPath[filepath.Clean(s.Path)] = append(sessByPath[filepath.Clean(s.Path)], n)
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
		if a == nil {
			continue
		}
		it := AgentPlanItem{AgentID: a.ID, Session: a.DevxSession, Adopted: a.Adopted, Retired: a.RetiredAt != nil, Marker: "none"}
		s := sessions[a.DevxSession]
		if s != nil {
			switch {
			case s.LocalOnly != nil && s.LocalOnly.AgentID == a.ID:
				it.Marker = "local_only"
			case s.ManagedAgent == a.ID:
				it.Marker = "managed_agent"
			case s.LocalOnly != nil || s.ManagedAgent != "":
				it.Marker = "other_agent"
			}
			if !s.CreatedAt.IsZero() && !a.CreatedAt.IsZero() {
				it.SessionVsAgent = s.CreatedAt.Sub(a.CreatedAt).Round(time.Millisecond).String()
			}
		}
		skip := func(reason string) {
			it.Action, it.Reason = ActionSkip, reason
			p.Agents = append(p.Agents, it)
		}
		if a.SessionInstanceID == "" && recorded != nil && a.RetiredAt == nil {
			if rid, rat, err := recorded(a.ID); err == nil && rid != "" {
				// The binding was dropped from agent.json. Restore it only if
				// the session record is still that exact instance.
				if s != nil && session.MatchesBoundInstance(s, rid, rat) &&
					filepath.Clean(s.Path) == filepath.Clean(a.Worktree) {
					it.Action, it.SessionInstanceID, it.SessionCreatedAt = ActionRestoreBinding, rid, rat
					if s.InstanceID == "" {
						restoredBy[a.DevxSession] = rid
						for i := range p.Sessions {
							if p.Sessions[i].Session == a.DevxSession && p.Sessions[i].Action == ActionAssignSessionID {
								p.Sessions[i].InstanceID, p.Sessions[i].Existing = rid, true
							}
						}
					}
				} else {
					it.Action, it.Reason = ActionSkip, SkipBoundElsewhere
				}
				p.Agents = append(p.Agents, it)
				continue
			}
		}
		if a.SessionInstanceID != "" {
			it.CurrentBindings = a.SessionInstanceID
			switch {
			case s == nil:
				skip(SkipNoSession)
			case s.InstanceID != "" && s.InstanceID != a.SessionInstanceID:
				skip(SkipBoundElsewhere)
			case s.InstanceID == "" && (a.SessionCreatedAt.IsZero() || !s.CreatedAt.Equal(a.SessionCreatedAt)):
				skip(SkipBoundIDMissing)
			case s.InstanceID == "" && (filepath.Clean(s.Path) != filepath.Clean(a.Worktree) || (s.ProjectAlias != "" && s.ProjectAlias != a.Project)):
				skip(SkipPathMismatch)
			case s.InstanceID == "" && restoredBy[a.DevxSession] != "" && restoredBy[a.DevxSession] != a.SessionInstanceID:
				skip(SkipDuplicateClaim)
			case s.InstanceID == "":
				// Old writer dropped the id: restore the SAME id the agent is
				// bound to instead of a fresh one.
				restoredBy[a.DevxSession] = a.SessionInstanceID
				for i := range p.Sessions {
					if p.Sessions[i].Session == a.DevxSession && p.Sessions[i].Action == ActionAssignSessionID {
						p.Sessions[i].InstanceID, p.Sessions[i].Existing = a.SessionInstanceID, true
						proposed[a.DevxSession] = a.SessionInstanceID
					}
				}
				it.Action = ActionBound
				p.Agents = append(p.Agents, it)
			default:
				it.Action = ActionBound
				p.Agents = append(p.Agents, it)
			}
			continue
		}
		switch {
		case a.RetiredAt != nil:
			skip(SkipRetired)
			continue
		case s == nil:
			skip(SkipNoSession)
			continue
		case s.Path == "" || !filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != filepath.Clean(a.Worktree):
			skip(SkipPathMismatch)
			continue
		case s.ProjectAlias != "" && s.ProjectAlias != a.Project:
			skip(SkipProjectMismatch)
			continue
		case s.IsContainerized():
			skip(SkipContainer)
			continue
		case s.LocalOnly != nil && s.ManagedAgent != "":
			skip(SkipBothMarkers)
			continue
		case it.Marker == "other_agent":
			skip(SkipForeignMarker)
			continue
		case len(live[a.DevxSession]) != 1:
			skip(SkipDuplicateClaim)
			continue
		case len(byPath[filepath.Clean(a.Worktree)]) != 1 || len(sessByPath[filepath.Clean(a.Worktree)]) != 1:
			skip(SkipSharedWorktree)
			continue
		}
		// Ordering, for EVERY marker type: every flow that binds an agent
		// writes the session record first, so a session newer than its
		// agent was recreated afterwards. A marker is not enough: before
		// instance ids, relaunching an adopted agent re-marked whatever
		// session had the name. Fail closed.
		if s.CreatedAt.IsZero() || a.CreatedAt.IsZero() {
			skip(SkipNoTimes)
			continue
		}
		if s.CreatedAt.After(a.CreatedAt) {
			skip(SkipSessionNewer)
			continue
		}
		// A record that already has an id was either given it by THIS
		// migration (an earlier, interrupted apply: the id is the keyed
		// derivation of this exact record) or created by an instance-aware
		// build after this legacy agent existed (a recreation). Only the
		// former may be bound.
		if s.InstanceID != "" && s.InstanceID != newID(a.DevxSession) {
			skip(SkipSessionHasID)
			continue
		}
		id, ok := proposed[a.DevxSession]
		if !ok {
			skip(SkipInvalidSessionID)
			continue
		}
		it.Action, it.SessionInstanceID, it.SessionCreatedAt = ActionBindAgent, id, s.CreatedAt
		p.Agents = append(p.Agents, it)
	}
	for _, it := range p.Sessions {
		p.Summary["sessions_"+it.Action]++
	}
	for _, it := range p.Agents {
		p.Summary["agents_"+it.Action]++
	}
	p.Notes = []string{
		"Only the fields listed are written: session.instance_id, agent.session_instance_id and agent.session_created_at.",
		"No worktree, tmux session, Pi conversation, lease, marker or other field is changed.",
		"Skipped agents are left unchanged; resolve them explicitly (retire, adopt again, or fix the record) and re-plan.",
	}
	p.Hash = p.computeHash()
	return p
}

func (p *InstancePlan) computeHash() string {
	cp := *p
	cp.Hash = ""
	b, _ := json.Marshal(cp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:16]
}

// VerifyHash reports whether the plan content still matches its hash.
func (p *InstancePlan) VerifyHash() bool { return p.Hash != "" && p.Hash == p.computeHash() }

// HasWork reports whether applying the plan would change anything.
func (p *InstancePlan) HasWork() bool {
	for _, s := range p.Sessions {
		if s.Action == ActionAssignSessionID {
			return true
		}
	}
	for _, a := range p.Agents {
		if a.Action == ActionBindAgent || a.Action == ActionRestoreBinding {
			return true
		}
	}
	return false
}

// JournalEntry is one applied step (newline-delimited JSON).
type JournalEntry struct {
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"` // session | agent
	Name       string    `json:"name"`
	InstanceID string    `json:"instance_id"`
	Restored   bool      `json:"restored_existing_id,omitempty"`
}

// ErrPlanStale means the records changed since the plan was made.
var ErrPlanStale = errors.New("records changed since the plan was made; re-run the dry run and review the new plan")

// InstanceStores is the narrow persistence surface the migration needs.
type InstanceStores struct {
	// MutateSessions applies fn to the latest session records under the
	// sessions lock and persists them atomically.
	MutateSessions func(fn func(map[string]*session.Session) error) error
	// LoadSessions reads the latest session records (no lock needed: the
	// sessions file is replaced atomically).
	LoadSessions func() (map[string]*session.Session, error)
	Agents       *Store
	// Journal appends one entry durably (fsync) before returning.
	Journal func(JournalEntry) error
	Now     func() time.Time
}

// ApplyInstancePlan writes the plan. Every step re-checks its precondition
// against the LATEST records under the relevant lock and is idempotent:
// a step already done (same value present) is skipped; a step whose
// precondition no longer holds aborts with ErrPlanStale before writing.
// Session ids are written first (one atomic sessions write), then each agent
// binding under that agent's lock.
func ApplyInstancePlan(p *InstancePlan, st InstanceStores) error {
	if !p.VerifyHash() {
		return fmt.Errorf("plan content does not match its hash")
	}
	now := st.Now
	if now == nil {
		now = time.Now
	}
	var journal []JournalEntry
	err := st.MutateSessions(func(sessions map[string]*session.Session) error {
		for _, it := range p.Sessions {
			if it.Action != ActionAssignSessionID {
				continue
			}
			s := sessions[it.Session]
			if s == nil {
				return fmt.Errorf("session %q: %w", it.Session, ErrPlanStale)
			}
			if !s.CreatedAt.Equal(it.CreatedAt) || filepath.Clean(s.Path) != filepath.Clean(it.Path) {
				return fmt.Errorf("session %q is not the reviewed record (recreated or changed): %w", it.Session, ErrPlanStale)
			}
			switch s.InstanceID {
			case it.InstanceID:
				continue // already applied
			case "":
				s.InstanceID = it.InstanceID
				journal = append(journal, JournalEntry{Time: now(), Kind: "session", Name: it.Session, InstanceID: it.InstanceID, Restored: it.Existing})
			default:
				return fmt.Errorf("session %q already has a different instance id: %w", it.Session, ErrPlanStale)
			}
		}
		// Journal before the sessions file is replaced: a crash after this
		// point leaves journal entries for writes that may not have
		// happened, which rollback treats as no-ops (value check).
		for _, j := range journal {
			if err := st.Journal(j); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, it := range p.Agents {
		if it.Action != ActionBindAgent && it.Action != ActionRestoreBinding {
			continue
		}
		if err := bindAgentChecked(st, it, now); err != nil {
			return err
		}
	}
	return nil
}

func bindAgentChecked(st InstanceStores, it AgentPlanItem, now func() time.Time) error {
	return st.Agents.WithAgentLock(it.AgentID, func() error {
		a, err := st.Agents.LoadAgent(it.AgentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", it.AgentID, err)
		}
		if a.SessionInstanceID == it.SessionInstanceID {
			return nil // already applied
		}
		if a.SessionInstanceID != "" || a.RetiredAt != nil || a.DevxSession != it.Session {
			return fmt.Errorf("agent %s: %w", it.AgentID, ErrPlanStale)
		}
		// The session must (still) carry exactly the planned id.
		sessions, err := st.LoadSessions()
		if err != nil {
			return err
		}
		cur := sessions[it.Session]
		if cur == nil || cur.InstanceID != it.SessionInstanceID || !session.MatchesBoundInstance(cur, it.SessionInstanceID, it.SessionCreatedAt) ||
			(!it.SessionCreatedAt.IsZero() && !cur.CreatedAt.Equal(it.SessionCreatedAt)) ||
			filepath.Clean(cur.Path) != filepath.Clean(a.Worktree) {
			return fmt.Errorf("agent %s session %q: %w", it.AgentID, it.Session, ErrPlanStale)
		}
		if err := st.Journal(JournalEntry{Time: now(), Kind: "agent", Name: it.AgentID, InstanceID: it.SessionInstanceID}); err != nil {
			return err
		}
		a.SessionInstanceID, a.SessionCreatedAt = it.SessionInstanceID, it.SessionCreatedAt
		if err := st.Agents.SaveAgent(a); err != nil {
			return err
		}
		_, err = st.Agents.AppendEvent(a.ID, Event{Type: "session_instance_bound", Source: "devx", Data: map[string]any{
			"session": it.Session, "session_instance_id": it.SessionInstanceID,
			"session_created_at": it.SessionCreatedAt.Format(time.RFC3339Nano), "via": "devx session instances"}})
		return err
	})
}

// RollbackInstances removes exactly the values recorded in the journal,
// and only where the record still holds that same value (anything changed
// since is left alone and reported). Agent bindings are removed first.
func RollbackInstances(entries []JournalEntry, st InstanceStores) (reverted int, skipped []string, err error) {
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind != "agent" {
			continue
		}
		err := st.Agents.WithAgentLock(e.Name, func() error {
			a, err := st.Agents.LoadAgent(e.Name)
			if err != nil {
				return err
			}
			if a.SessionInstanceID != e.InstanceID {
				skipped = append(skipped, "agent "+e.Name+": binding changed since migration")
				return nil
			}
			a.SessionInstanceID, a.SessionCreatedAt = "", time.Time{}
			if err := st.Agents.SaveAgent(a); err != nil {
				return err
			}
			reverted++
			_, err = st.Agents.AppendEvent(a.ID, Event{Type: "session_instance_unbound", Source: "devx", Data: map[string]any{
				"session": a.DevxSession, "via": "devx session instances --rollback"}})
			return err
		})
		if err != nil && !errors.Is(err, ErrNotFound) {
			return reverted, skipped, err
		}
	}
	err = st.MutateSessions(func(sessions map[string]*session.Session) error {
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.Kind != "session" {
				continue
			}
			s := sessions[e.Name]
			switch {
			case s == nil:
				skipped = append(skipped, "session "+e.Name+": removed since migration")
			case e.Restored:
				// The id was the agent's existing binding, restored after an
				// old writer dropped it: leave it (removing it would break
				// that binding).
				skipped = append(skipped, "session "+e.Name+": restored pre-existing id kept")
			case s.InstanceID != e.InstanceID:
				skipped = append(skipped, "session "+e.Name+": instance id changed since migration")
			default:
				s.InstanceID = ""
				reverted++
			}
		}
		return nil
	})
	return reverted, skipped, err
}
