package piagent

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Store is the on-disk state shared with the bridge extension. Layout:
//
//	<root>/agents/<agent-id>/agent.json        Agent record (lease, binding)
//	<root>/agents/<agent-id>/tasks/<id>.json   Task records
//	<root>/agents/<agent-id>/results/<id>.txt  Final assistant text per task
//	<root>/agents/<agent-id>/events.jsonl      Durable event log (seq ordered)
//	<root>/agents/<agent-id>/seq               Last event seq
//	<root>/agents/<agent-id>/bridge.json       Bridge heartbeat (bridge-owned)
//	<root>/agents/<agent-id>/agent.lock/       Shared mkdir lock
//	<root>/idempotency/<key-hash>.json         Start/send idempotency records
type Store struct {
	Root string
	now  func() time.Time
}

func NewStore(root string) *Store {
	return &Store{Root: root, now: func() time.Time { return time.Now().UTC() }}
}

var (
	agentIDPattern = regexp.MustCompile(`^pa_[0-9a-f]{12}$`)
	taskIDPattern  = regexp.MustCompile(`^pt_[0-9a-f]{16}$`)
)

// ErrNotFound reports an unknown agent or task.
var ErrNotFound = errors.New("not found")

func ValidAgentID(id string) bool { return agentIDPattern.MatchString(id) }
func ValidTaskID(id string) bool  { return taskIDPattern.MatchString(id) }

func newAgentID() string { return "pa_" + randomHex(6) }
func newTaskID() string  { return "pt_" + randomHex(8) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// newPiSessionID returns a UUIDv7 string for Pi's --session-id.
func newPiSessionID(now time.Time) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	ms := uint64(now.UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (40 - 8*i))
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func (s *Store) agentsDir() string             { return filepath.Join(s.Root, "agents") }
func (s *Store) AgentDir(id string) string     { return filepath.Join(s.agentsDir(), id) }
func (s *Store) agentPath(id string) string    { return filepath.Join(s.AgentDir(id), "agent.json") }
func (s *Store) tasksDir(id string) string     { return filepath.Join(s.AgentDir(id), "tasks") }
func (s *Store) resultsDir(id string) string   { return filepath.Join(s.AgentDir(id), "results") }
func (s *Store) eventsPath(id string) string   { return filepath.Join(s.AgentDir(id), "events.jsonl") }
func (s *Store) seqPath(id string) string      { return filepath.Join(s.AgentDir(id), "seq") }
func (s *Store) bridgePath(id string) string   { return filepath.Join(s.AgentDir(id), "bridge.json") }
func (s *Store) lockPath(id string) string     { return filepath.Join(s.AgentDir(id), agentLockName) }
func (s *Store) idemDir() string               { return filepath.Join(s.Root, "idempotency") }
func (s *Store) taskPath(a, t string) string   { return filepath.Join(s.tasksDir(a), t+".json") }
func (s *Store) resultPath(a, t string) string { return filepath.Join(s.resultsDir(a), t+".txt") }

func (s *Store) ensureAgentDirs(id string) error {
	for _, dir := range []string{s.tasksDir(id), s.resultsDir(id)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// WithAgentLock runs fn while holding the lock shared with the bridge.
func (s *Store) WithAgentLock(id string, fn func() error) error {
	if !ValidAgentID(id) {
		return fmt.Errorf("invalid agent id %q", id)
	}
	if err := os.MkdirAll(s.AgentDir(id), 0o700); err != nil {
		return err
	}
	release, err := acquireDirLock(s.lockPath(id), agentLockTimeout, agentLockStale)
	if err != nil {
		return fmt.Errorf("agent %s: %w", id, err)
	}
	defer release()
	return fn()
}

func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.%s.tmp", path, os.Getpid(), randomHex(4))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Store) LoadAgent(id string) (*Agent, error) {
	if !ValidAgentID(id) {
		return nil, fmt.Errorf("invalid agent id %q", id)
	}
	var a Agent
	if err := readJSON(s.agentPath(id), &a); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("agent %s: %w", id, ErrNotFound)
		}
		return nil, err
	}
	return &a, nil
}

func (s *Store) SaveAgent(a *Agent) error {
	a.UpdatedAt = s.now()
	return writeJSONAtomic(s.agentPath(a.ID), a)
}

func (s *Store) ListAgents() ([]*Agent, error) {
	entries, err := os.ReadDir(s.agentsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Agent
	for _, e := range entries {
		if !e.IsDir() || !ValidAgentID(e.Name()) {
			continue
		}
		a, err := s.LoadAgent(e.Name())
		if errors.Is(err, ErrNotFound) {
			continue // start in progress
		}
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) LoadTask(agentID, taskID string) (*Task, error) {
	if !ValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id %q", taskID)
	}
	var t Task
	if err := readJSON(s.taskPath(agentID, taskID), &t); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("task %s: %w", taskID, ErrNotFound)
		}
		return nil, err
	}
	return &t, nil
}

func (s *Store) SaveTask(t *Task) error {
	t.UpdatedAt = s.now()
	return writeJSONAtomic(s.taskPath(t.AgentID, t.ID), t)
}

// FindTask locates a task by ID across managed agents.
func (s *Store) FindTask(taskID string) (*Task, error) {
	if !ValidTaskID(taskID) {
		return nil, fmt.Errorf("invalid task id %q", taskID)
	}
	entries, err := os.ReadDir(s.agentsDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() || !ValidAgentID(e.Name()) {
			continue
		}
		t, err := s.LoadTask(e.Name(), taskID)
		if err == nil {
			return t, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("task %s: %w", taskID, ErrNotFound)
}

func (s *Store) ListTasks(agentID string) ([]*Task, error) {
	entries, err := os.ReadDir(s.tasksDir(agentID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || !ValidTaskID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		t, err := s.LoadTask(agentID, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out, nil
}

func (s *Store) LoadBridge(agentID string) (*Bridge, error) {
	var b Bridge
	if err := readJSON(s.bridgePath(agentID), &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// AppendEvent appends to the durable event log. Callers must hold the agent
// lock; the bridge appends under the same lock so seq values never collide.
func (s *Store) AppendEvent(agentID string, ev Event) (Event, error) {
	last := int64(0)
	if data, err := os.ReadFile(s.seqPath(agentID)); err == nil {
		last, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	} else if !os.IsNotExist(err) {
		return ev, err
	}
	ev.Seq = last + 1
	if ev.Time.IsZero() {
		ev.Time = s.now()
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	f, err := os.OpenFile(s.eventsPath(agentID), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return ev, err
	}
	// A writer that crashed mid-line leaves a torn tail. Start on a fresh
	// line so this event is not glued onto it and lost.
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return ev, err
	}
	if err := f.Close(); err != nil {
		return ev, err
	}
	// The log is the source of truth; seq is a cache. A crash between the
	// two writes is repaired by ReadEvents tolerating duplicate seq values
	// and by lastSeqFromLog on the next append.
	return ev, writeFileAtomic(s.seqPath(agentID), []byte(strconv.FormatInt(ev.Seq, 10)))
}

// ReadEvents returns up to limit events with Seq > after, plus the last seq
// in the log. Events are returned in log order; a missed or replayed event is
// recovered by re-reading from the client's last acknowledged seq.
func (s *Store) ReadEvents(agentID string, after int64, limit int) ([]Event, int64, error) {
	if limit <= 0 || limit > MaxEventsPage {
		limit = MaxEventsPage
	}
	f, err := os.Open(s.eventsPath(agentID))
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var out []Event
	var lastSeq int64
	seen := map[int64]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // torn final line from a crashed writer
		}
		if ev.Seq > lastSeq {
			lastSeq = ev.Seq
		}
		if ev.Seq <= after || seen[ev.Seq] || len(out) >= limit {
			continue
		}
		seen[ev.Seq] = true
		out = append(out, ev)
	}
	return out, lastSeq, sc.Err()
}

// ReadResult returns the redacted final text for a task.
func (s *Store) ReadResult(agentID, taskID string) (string, bool, error) {
	data, err := os.ReadFile(s.resultPath(agentID, taskID))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return Redact(string(data)), true, nil
}

// idempotency records

type idemRecord struct {
	Kind            string    `json:"kind"`
	KeyHash         string    `json:"key_hash"`
	RequestHash     string    `json:"request_hash"`
	AgentID         string    `json:"agent_id"`
	TaskID          string    `json:"task_id"`
	PiSessionID     string    `json:"pi_session_id,omitempty"`
	SessionName     string    `json:"session_name,omitempty"`
	CreatingSession bool      `json:"creating_session,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

func (s *Store) idemPath(keyHash string) string {
	return filepath.Join(s.idemDir(), keyHash+".json")
}

func (s *Store) loadIdem(keyHash string) (*idemRecord, error) {
	var r idemRecord
	if err := readJSON(s.idemPath(keyHash), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) saveIdem(r *idemRecord) error { return writeJSONAtomic(s.idemPath(r.KeyHash), r) }

func (s *Store) withIdemLock(keyHash string, fn func() error) error {
	if err := os.MkdirAll(s.idemDir(), 0o700); err != nil {
		return err
	}
	// Starts can take tens of seconds (worktree + tmux + Pi launch), so the
	// per-key lock waits longer and is considered stale later.
	release, err := acquireDirLock(filepath.Join(s.idemDir(), keyHash+".lock"), 90*time.Second, 180*time.Second)
	if err != nil {
		return fmt.Errorf("idempotency key busy: %w", err)
	}
	defer release()
	return fn()
}
