package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mainlineSession is session.Session exactly as on origin/main e22869d (before
// the local-only marker and instance ids). It stands in for an older DevX
// binary that rewrites sessions.json.
type mainlineSession struct {
	Name               string            `json:"name"`
	ProjectAlias       string            `json:"project_alias,omitempty"`
	ProjectPath        string            `json:"project_path,omitempty"`
	Branch             string            `json:"branch"`
	Path               string            `json:"path"`
	Ports              map[string]int    `json:"ports"`
	Routes             map[string]string `json:"routes,omitempty"`
	EditorPID          int               `json:"editor_pid,omitempty"`
	AttentionFlag      bool              `json:"attention_flag,omitempty"`
	AttentionReason    string            `json:"attention_reason,omitempty"`
	AttentionSource    string            `json:"attention_source,omitempty"`
	AttentionTime      time.Time         `json:"attention_time,omitempty"`
	DisplayName        string            `json:"display_name,omitempty"`
	Pinned             bool              `json:"pinned,omitempty"`
	LastAttached       time.Time         `json:"last_attached,omitempty"`
	LastArtifactSeenAt time.Time         `json:"last_artifact_seen_at,omitempty"`
	LastReviewedAt     time.Time         `json:"last_reviewed_at,omitempty"`
	Review             *SessionReview    `json:"review,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	Target             TargetMeta        `json:"target,omitempty"`
}

type mainlineStore struct {
	Sessions      map[string]*mainlineSession `json:"sessions"`
	NumberedSlots map[int]string              `json:"numbered_slots,omitempty"`
}

const markedRecord = `{"sessions":{"lo":{"name":"lo","branch":"lo","path":"/w/lo","ports":{},
"created_at":"2026-10-08T22:32:42.117391-07:00","updated_at":"2026-10-08T22:32:42.117391-07:00",
"instance_id":"si_0123456789abcdef01234567",
"local_only":{"owner":"devx-pi-mcp","agent_id":"pa_000000000001","created_at":"2026-10-08T22:32:42.117391-07:00"}}}}`

// Reproduces the incident mechanism: an older-schema writer's full
// read-modify-write drops local_only and instance_id from the record.
func TestMainlineSchemaRoundtripDropsOwnershipMarker(t *testing.T) {
	var old mainlineStore
	if err := json.Unmarshal([]byte(markedRecord), &old); err != nil {
		t.Fatal(err)
	}
	old.Sessions["lo"].Pinned = true // any unrelated mutation
	out, _ := json.Marshal(old)
	if strings.Contains(string(out), "local_only") || strings.Contains(string(out), "instance_id") {
		t.Fatalf("older schema unexpectedly preserved the marker: %s", out)
	}
}

// This build keeps members it does not know, so a FUTURE field survives a
// write by this build, and known fields (the marker) round-trip exactly.
func TestCurrentSchemaPreservesUnknownFieldsAndMarker(t *testing.T) {
	setupTempHome(t)
	path := getSessionsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	future := strings.Replace(markedRecord, `"instance_id"`, `"future_owner":{"v":2,"who":"x"},"instance_id"`, 1)
	future = strings.Replace(future, `{"sessions"`, `{"schema_hint":"v9","sessions"`, 1)
	if err := os.WriteFile(path, []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSession("lo", func(s *Session) { s.Pinned = true }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var raw struct {
		SchemaHint string                                `json:"schema_hint"`
		Sessions   map[string]map[string]json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.SchemaHint != "v9" {
		t.Fatalf("top-level unknown member dropped: %s", data)
	}
	rec := raw.Sessions["lo"]
	var compact bytes.Buffer
	_ = json.Compact(&compact, rec["future_owner"])
	if compact.String() != `{"v":2,"who":"x"}` {
		t.Fatalf("unknown record member not preserved: %s", rec["future_owner"])
	}
	if string(rec["pinned"]) != "true" {
		t.Fatal("mutation lost")
	}
	re, _ := LoadSessions()
	s := re.Sessions["lo"]
	if s.LocalOnly == nil || s.LocalOnly.AgentID != "pa_000000000001" || s.InstanceID != "si_0123456789abcdef01234567" {
		t.Fatalf("marker or instance id lost: %+v", s)
	}
}

// Known fields always win over a preserved unknown member of the same key,
// and an explicitly retired field ("color") is still dropped.
func TestUnknownFieldsNeverShadowKnownOrRetired(t *testing.T) {
	var s Session
	if err := json.Unmarshal([]byte(`{"name":"a","color":"purple","x":1}`), &s); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Unknown["color"]; ok {
		t.Fatal("retired field must not be preserved")
	}
	s.Unknown["name"] = json.RawMessage(`"shadow"`)
	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	if back["name"] != "a" || back["x"] != float64(1) {
		t.Fatalf("got %s", out)
	}
	// Removing a known field clears it even if Unknown would supply it.
	s.LocalOnly = nil
	s.Unknown = map[string]json.RawMessage{}
	out, _ = json.Marshal(s)
	if strings.Contains(string(out), "local_only") {
		t.Fatalf("cleared marker reappeared: %s", out)
	}
}
