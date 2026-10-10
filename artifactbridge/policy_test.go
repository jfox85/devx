package artifactbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

// --- parsing --------------------------------------------------------------------

func TestParsePolicyDefaultsAndExplicit(t *testing.T) {
	p, err := ParsePolicy([]byte("pi_mcp:\n  allowed_projects: [a, b]\n"))
	if err != nil || !p.Read || p.Upload || p.SessionsSet || len(p.AllowedProjects) != 2 {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	p, err = ParsePolicy([]byte("web_port: 7777\n")) // no pi_mcp at all
	if err != nil || !p.Read || len(p.AllowedProjects) != 0 || p.readEligible("x", "a") {
		t.Fatalf("no pi_mcp must make nothing eligible: %+v %v", p, err)
	}
	p, err = ParsePolicy([]byte(`
pi_mcp:
  allowed_projects: [a]
  artifacts:
    read: true
    upload: false
    sessions: [fixture]
    exclude_sessions: [secret]
    exclude_projects: [b]
    max_upload_bytes: 2048
`))
	if err != nil || !p.SessionsSet || p.Sessions[0] != "fixture" || p.ExcludeSessions[0] != "secret" || p.MaxUploadBytes != 2048 {
		t.Fatalf("explicit: %+v %v", p, err)
	}
	// Explicit empty list = nothing.
	p, _ = ParsePolicy([]byte("pi_mcp:\n  allowed_projects: [a]\n  artifacts:\n    sessions: []\n"))
	if !p.SessionsSet || p.readEligible("any", "a") {
		t.Fatalf("explicit empty list must expose nothing: %+v", p)
	}
	// The live fixture-only config (activation of 2026-10-10) keeps its scope.
	live := "pi_mcp:\n  allowed_projects: [agentshed-sandbox, devx, nibit]\n  pi_args: [\"--model\", \"x\"]\n  artifacts:\n    read: true\n    upload: false\n    sessions: [artifact-bridge-fixture]\n"
	p, err = ParsePolicy([]byte(live))
	if err != nil || !p.readEligible("artifact-bridge-fixture", "agentshed-sandbox") || p.readEligible("devx-artifacts-opus55-oct9", "devx") {
		t.Fatalf("existing fixture-only config widened: %+v %v", p, err)
	}
}

func TestParsePolicyFailsClosed(t *testing.T) {
	bad := map[string]string{
		"syntax":           "pi_mcp: [\n",
		"root list":        "- a\n",
		"pi_mcp scalar":    "pi_mcp: yes\n",
		"artifacts scalar": "pi_mcp:\n  artifacts: true\n",
		"typo key":         "pi_mcp:\n  allowed_projects: [a]\n  artifacts:\n    session: [fixture]\n",
		"read string":      "pi_mcp:\n  artifacts:\n    read: \"yes\"\n",
		"read null":        "pi_mcp:\n  artifacts:\n    read:\n",
		"sessions scalar":  "pi_mcp:\n  artifacts:\n    sessions: fixture\n",
		"sessions null":    "pi_mcp:\n  artifacts:\n    sessions:\n",
		"sessions number":  "pi_mcp:\n  artifacts:\n    sessions: [1]\n",
		"sessions empty":   "pi_mcp:\n  artifacts:\n    sessions: [\"\"]\n",
		"dup key":          "pi_mcp:\n  artifacts:\n    sessions: [a]\n    sessions: [b]\n",
		"dup pi_mcp":       "pi_mcp:\n  allowed_projects: [a]\npi_mcp:\n  allowed_projects: [b]\n",
		"alias":            "x: &s [a]\npi_mcp:\n  artifacts:\n    sessions: *s\n",
		"merge":            "base: &b {read: true}\npi_mcp:\n  artifacts:\n    <<: *b\n",
		"projects scalar":  "pi_mcp:\n  allowed_projects: a\n",
		"max negative":     "pi_mcp:\n  artifacts:\n    max_upload_bytes: -1\n",
		"max string":       "pi_mcp:\n  artifacts:\n    max_upload_bytes: \"10\"\n",
	}
	for name, doc := range bad {
		if _, err := ParsePolicy([]byte(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoadPolicyFile(t *testing.T) {
	dir := t.TempDir()
	if p, err := LoadPolicyFile(filepath.Join(dir, "missing.yaml")); err != nil || len(p.AllowedProjects) != 0 {
		t.Fatalf("missing file: %+v %v", p, err)
	}
	if _, err := LoadPolicyFile(""); err == nil {
		t.Fatal("empty path must fail")
	}
	if _, err := LoadPolicyFile(dir); err == nil {
		t.Fatal("directory must fail")
	}
	unreadable := filepath.Join(dir, "u.yaml")
	_ = os.WriteFile(unreadable, []byte("pi_mcp: {}\n"), 0o000)
	if os.Getuid() != 0 {
		if _, err := LoadPolicyFile(unreadable); err == nil {
			t.Fatal("unreadable file must fail")
		}
	}
	big := filepath.Join(dir, "big.yaml")
	_ = os.WriteFile(big, []byte(strings.Repeat("#", maxPolicyFileBytes+1)), 0o600)
	if _, err := LoadPolicyFile(big); err == nil {
		t.Fatal("oversized config must fail")
	}
	// Writable by group or others: another local user could widen scope.
	good := "pi_mcp:\n  allowed_projects: [proj]\n"
	for _, mode := range []os.FileMode{0o620, 0o602, 0o666} {
		p := filepath.Join(dir, fmt.Sprintf("w%o.yaml", mode))
		_ = os.WriteFile(p, []byte(good), 0o600)
		_ = os.Chmod(p, mode)
		if _, err := LoadPolicyFile(p); err == nil {
			t.Fatalf("mode %o must fail", mode)
		}
	}
	ok := filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(ok, []byte(good), 0o644)
	if p, err := LoadPolicyFile(ok); err != nil || len(p.AllowedProjects) != 1 {
		t.Fatalf("0644 owner config must load: %+v %v", p, err)
	}
	// A FIFO must not block the loader.
	fifo := filepath.Join(dir, "fifo.yaml")
	if err := mkfifo(fifo); err == nil {
		if _, err := LoadPolicyFile(fifo); err == nil {
			t.Fatal("fifo config must fail")
		}
	}
}

// --- runtime behaviour (no restart between steps) -------------------------------

// defaultScope removes the explicit sessions list (default-wide read scope).
func (e *env) defaultScope() {
	e.polMu.Lock()
	defer e.polMu.Unlock()
	e.pol.Sessions, e.pol.SessionsSet = nil, false
}

func TestDefaultScopeFollowsMCPVisibleSessionsAtRuntime(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	e.pol.Upload = false
	a1, s1 := e.agent("s1", "proj")
	e.register(s1, "One", "one.md", []byte("one"))
	if len(e.list(a1.ID)) != 1 {
		t.Fatal("default scope must cover an MCP-visible session in an allowed project")
	}
	// A new managed session appears: eligible on the next call, no restart.
	a2, s2 := e.agent("s2", "proj")
	e.register(s2, "Two", "two.md", []byte("two"))
	if len(e.list(a2.ID)) != 1 {
		t.Fatal("newly added session must be eligible immediately")
	}
	// Session removed: denied on the next call.
	delete(e.sessions, "s2")
	if _, err := e.svc.List(ListRequest{AgentID: a2.ID}); codeOf(err) != codeDenied {
		t.Fatalf("removed session: %v", err)
	}
	// Agent retired: denied.
	now := e.now
	a1.RetiredAt = &now
	_ = e.store.WithAgentLock(a1.ID, func() error { return e.store.SaveAgent(a1) })
	if _, err := e.svc.List(ListRequest{AgentID: a1.ID}); codeOf(err) != codeDenied {
		t.Fatalf("retired agent: %v", err)
	}
}

func TestDefaultScopeAcceptsMissingMarkerButNotForeignMarker(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	a, s := e.agent("legacy", "proj")
	e.register(s, "One", "one.md", []byte("one"))
	// Legacy MCP-created / adopted-with-dropped-marker record: no marker.
	s.ManagedAgent = ""
	if len(e.list(a.ID)) != 1 {
		t.Fatal("a missing marker alone must not deny (the MCP path does not require one)")
	}
	// A marker naming another agent is a conflict, never overridden.
	s.ManagedAgent = "pa_ffffffffffff"
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("foreign managed marker: %v", err)
	}
	s.ManagedAgent = ""
	s.LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_ffffffffffff"}
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("foreign local-only marker: %v", err)
	}
	// Explicit lists do not override a conflict either.
	e.pol.Sessions, e.pol.SessionsSet = []string{"legacy"}, true
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("explicit list must not override a foreign marker: %v", err)
	}
}

func TestExplicitAllowlistAndExclusionsAtRuntime(t *testing.T) {
	e := newEnv(t)
	a1, s1 := e.agent("s1", "proj")
	a2, s2 := e.agent("s2", "other")
	e.register(s1, "One", "one.md", []byte("one"))
	e.register(s2, "Two", "two.md", []byte("two"))
	// Explicit list naming only s1.
	e.pol.Sessions = []string{"s1"}
	if _, err := e.svc.List(ListRequest{AgentID: a2.ID}); codeOf(err) != codeDenied {
		t.Fatalf("explicit allowlist must restrict: %v", err)
	}
	// Add s2 to the list at runtime.
	e.pol.Sessions = []string{"s1", "s2"}
	if len(e.list(a2.ID)) != 1 {
		t.Fatal("session added to explicit list must be readable immediately")
	}
	// Exclusions subtract from explicit lists...
	e.pol.ExcludeSessions = []string{"s2"}
	if _, err := e.svc.List(ListRequest{AgentID: a2.ID}); codeOf(err) != codeDenied {
		t.Fatalf("exclude_sessions: %v", err)
	}
	e.pol.ExcludeSessions = nil
	e.pol.ExcludeProjects = []string{"proj"}
	if _, err := e.svc.List(ListRequest{AgentID: a1.ID}); codeOf(err) != codeDenied {
		t.Fatalf("exclude_projects: %v", err)
	}
	// ...and from the default-wide scope.
	e.defaultScope()
	if _, err := e.svc.List(ListRequest{AgentID: a1.ID}); codeOf(err) != codeDenied {
		t.Fatalf("exclude_projects in default scope: %v", err)
	}
	if len(e.list(a2.ID)) != 1 {
		t.Fatal("non-excluded project should stay readable")
	}
	// Allowed-project change at runtime.
	e.pol.ExcludeProjects = nil
	e.pol.AllowedProjects = []string{"proj"}
	if _, err := e.svc.List(ListRequest{AgentID: a2.ID}); codeOf(err) != codeDenied {
		t.Fatalf("project removed from allowed_projects: %v", err)
	}
	e.pol.AllowedProjects = []string{"proj", "other"}
	if len(e.list(a2.ID)) != 1 {
		t.Fatal("project re-added to allowed_projects")
	}
}

func TestRevocationBetweenChunks(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	a, s := e.agent("s1", "proj")
	e.register(s, "Big", "big.md", []byte(strings.Repeat("x", 50000)))
	it := e.list(a.ID)[0]
	read := func(off int64) (*ReadResult, error) {
		return e.svc.Read(ReadRequest{AgentID: a.ID, ArtifactID: it.ID, Version: it.Version, Offset: off})
	}
	r, err := read(0)
	if err != nil || r.EOF {
		t.Fatalf("first chunk: %v", err)
	}
	cases := []struct {
		name          string
		revoke, grant func()
	}{
		{"explicit list without session", func() { e.pol.Sessions, e.pol.SessionsSet = []string{"other"}, true }, e.defaultScope},
		{"excluded", func() { e.pol.ExcludeSessions = []string{"s1"} }, func() { e.pol.ExcludeSessions = nil }},
		{"read disabled", func() { e.pol.Read = false }, func() { e.pol.Read = true }},
		{"project removed", func() { e.pol.AllowedProjects = []string{"other"} }, func() { e.pol.AllowedProjects = []string{"proj", "other"} }},
		{"config unreadable", func() { e.polErr = errors.New("boom") }, func() { e.polErr = nil }},
		{"marker reassigned", func() { s.ManagedAgent = "pa_ffffffffffff" }, func() { s.ManagedAgent = a.ID }},
		{"worktree moved", func() { s.Path = s.Path + "-moved" }, func() { s.Path = a.Worktree }},
	}
	for _, c := range cases {
		c.revoke()
		if _, err := read(r.NextOffset); err == nil {
			t.Fatalf("%s: continuation chunk was served", c.name)
		}
		c.grant()
		if _, err := read(r.NextOffset); err != nil {
			t.Fatalf("%s: restore failed: %v", c.name, err)
		}
	}
}

func TestMalformedConfigDisablesEverything(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.register(s, "One", "one.md", []byte("one"))
	id := e.list(a.ID)[0].ID
	e.polErr = errors.New("parse error")
	if n := len(e.svc.Tools()); n != 0 {
		t.Fatalf("tools listed while config invalid: %d", n)
	}
	if e.svc.Instructions() != "" {
		t.Fatal("instructions while config invalid")
	}
	for _, name := range []string{ToolList, ToolRead, ToolUpload} {
		args, _ := json.Marshal(map[string]any{"agent_id": a.ID, "artifact_id": id})
		res, handled := e.svc.CallTool(name, args)
		if !handled || res["isError"] != true || res["structuredContent"].(map[string]any)["error"] != codeUnavailable {
			t.Fatalf("%s while config invalid: %v", name, res)
		}
		msg := res["structuredContent"].(map[string]any)["message"].(string)
		if strings.Contains(msg, "parse error") || strings.Contains(msg, "/") {
			t.Fatalf("config error leaked details: %q", msg)
		}
	}
	tv := &piagent.TaskView{DeliveredAt: &e.now}
	if e.svc.TaskArtifacts(a, tv) != nil {
		t.Fatal("task artifacts while config invalid")
	}
	// Fixed config restores service on the next call.
	e.polErr = nil
	if len(e.list(a.ID)) != 1 {
		t.Fatal("service not restored after config fixed")
	}
}

func TestUploadNeverUsesDefaultWideScope(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	e.defaultScope()
	e.pol.Upload = true
	if _, err := e.svc.Upload(uploadReq(a.ID, "k", "n.txt", "text/plain", []byte("hi"), 0, true)); codeOf(err) != codeDenied {
		t.Fatalf("upload with default scope: %v", err)
	}
	e.pol.Sessions, e.pol.SessionsSet = []string{"s1"}, true
	if _, err := e.svc.Upload(uploadReq(a.ID, "k", "n.txt", "text/plain", []byte("hi"), 0, true)); err != nil {
		t.Fatalf("explicitly listed upload: %v", err)
	}
	e.pol.ExcludeSessions = []string{"s1"}
	if _, err := e.svc.Upload(uploadReq(a.ID, "k2", "m.txt", "text/plain", []byte("hi"), 0, true)); codeOf(err) != codeDenied {
		t.Fatalf("excluded upload: %v", err)
	}
	m, _ := artifactpkg.LoadManifest(s)
	if len(m.Artifacts) != 1 {
		t.Fatalf("want 1 attachment, got %d", len(m.Artifacts))
	}
}

func TestToolListFollowsPolicyWithStableDefinitions(t *testing.T) {
	e := newEnv(t)
	defs := func() map[string]string {
		out := map[string]string{}
		for _, tl := range e.svc.Tools() {
			b, _ := json.Marshal(tl)
			out[tl.Name] = string(b)
		}
		return out
	}
	e.pol.Upload = false
	readOnly := defs()
	if len(readOnly) != 2 {
		t.Fatalf("read only: %v", readOnly)
	}
	e.pol.Upload, e.pol.MaxUploadBytes = true, 1234
	withUpload := defs()
	if len(withUpload) != 3 {
		t.Fatalf("with upload: %d", len(withUpload))
	}
	e.pol.MaxUploadBytes = 999999
	// Definitions must not change with policy values (would force gateway re-review).
	for name, d := range defs() {
		if withUpload[name] != d {
			t.Fatalf("%s definition depends on policy values", name)
		}
	}
	for name, d := range readOnly {
		if withUpload[name] != d {
			t.Fatalf("%s definition changed when upload toggled", name)
		}
	}
}
