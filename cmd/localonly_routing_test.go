package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/viper"
)

// TestDefaultRouteSyncNeverPublishesLocalOnly runs the normal (default
// config) Caddy and Cloudflare syncs over a store that holds an ordinary
// session and a local-only managed session whose record was edited to carry
// ports and routes (as a stale record or a hand edit would). The ordinary
// session's routes are produced unchanged; the local-only one appears in
// neither config, the health check, nor the web/agent views.
//
// External effects are fakes: `caddy` on PATH is a script that records its
// argv, caddy_api points at a recording httptest server on loopback, and
// the cloudflared pid file is absent so no daemon reload is attempted.
func TestDefaultRouteSyncNeverPublishesLocalOnly(t *testing.T) {
	requireIsolatedTestEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads USERPROFILE on Windows
	cfgDir := filepath.Join(home, ".config", "devx")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Fake caddy executable: records argv, never touches a real server.
	bin := filepath.Join(home, "bin")
	_ = os.MkdirAll(bin, 0o700)
	caddyLog := filepath.Join(home, "caddy-argv.log")
	_ = os.WriteFile(filepath.Join(bin, "caddy"), []byte("#!/bin/sh\necho \"$@\" >> "+caddyLog+"\nexit 0\n"), 0o700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var apiHits atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer api.Close()

	tunnelCfg := filepath.Join(home, "cloudflared.yaml")
	for k, v := range map[string]any{
		"disable_caddy": false, "caddy_api": api.URL, "external_domain": "example.test",
		"cloudflare_tunnel_id": "tunnel-test", "cloudflare_credentials_file": filepath.Join(home, "creds.json"),
		"cloudflare_tunnel_config": tunnelCfg,
	} {
		prev := viper.Get(k)
		viper.Set(k, v)
		t.Cleanup(func() { viper.Set(k, prev) })
	}

	repo := filepath.Join(home, "repo")
	_ = os.MkdirAll(repo, 0o700)
	reg := &config.ProjectRegistry{Projects: map[string]*config.Project{"proj": {Name: "proj", Path: repo}}}
	regData, _ := json.Marshal(reg)
	_ = os.WriteFile(filepath.Join(cfgDir, "projects.json"), regData, 0o600)
	now := time.Now()
	store := &session.SessionStore{Sessions: map[string]*session.Session{
		"normal": {Name: "normal", ProjectPath: repo, ProjectAlias: "proj", Branch: "normal", Path: filepath.Join(repo, ".worktrees", "normal"),
			Ports: map[string]int{"WEB": 41001}, Routes: map[string]string{"WEB": "proj-normal-web.localhost"}, CreatedAt: now},
		"managed": {Name: "managed", ProjectPath: repo, ProjectAlias: "proj", Branch: "managed", Path: filepath.Join(repo, ".worktrees", "managed"),
			Ports: map[string]int{"WEB": 41002, "API": 41003}, Routes: map[string]string{"WEB": "proj-managed-web.localhost"}, CreatedAt: now,
			LocalOnly: &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_x", CreatedAt: now}},
	}, NumberedSlots: map[int]string{}}
	if err := store.Overwrite(); err != nil {
		t.Fatal(err)
	}

	if err := syncAllCaddyRoutes(); err != nil {
		t.Fatal(err)
	}
	if err := syncAllCloudflareRoutes(); err != nil {
		t.Fatal(err)
	}
	caddyJSON, err := os.ReadFile(filepath.Join(cfgDir, "caddy-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	tunnel, err := os.ReadFile(tunnelCfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"caddy": string(caddyJSON), "cloudflared": string(tunnel)} {
		if strings.Contains(body, "managed") || strings.Contains(body, "41002") || strings.Contains(body, "41003") {
			t.Fatalf("%s config publishes the local-only session:\n%s", name, body)
		}
		if !strings.Contains(body, "normal") || !strings.Contains(body, "41001") {
			t.Fatalf("%s config lost the ordinary session's route:\n%s", name, body)
		}
	}
	if !strings.Contains(string(tunnel), "proj-normal-web.example.test") {
		t.Fatalf("ordinary external route missing:\n%s", tunnel)
	}
	// The fake caddy was asked to reload the file we checked; nothing else.
	if b, _ := os.ReadFile(caddyLog); !strings.HasPrefix(string(b), "reload --config ") {
		t.Fatalf("unexpected caddy argv: %q", b)
	}

	// Health check (talks only to the fake API): no route is expected for
	// the local-only session.
	reloaded, _ := session.LoadSessions()
	registry, _ := config.LoadProjectRegistry()
	result, err := caddy.CheckCaddyHealth(buildSessionInfoMap(reloaded, registry))
	if err != nil {
		t.Fatal(err)
	}
	for _, rs := range result.RouteStatuses {
		if rs.SessionName == "managed" {
			t.Fatalf("health check expects a route for local-only session: %+v", rs)
		}
	}
	if result.RoutesNeeded != 1 || apiHits.Load() == 0 {
		t.Fatalf("routes needed=%d api hits=%d", result.RoutesNeeded, apiHits.Load())
	}
}

// Removing a local-only session must not rewrite shared route configs or
// reload a tunnel (it was never in them), even with SyncRoutes requested.
func TestRemoveLocalOnlySessionSkipsSharedRouteSync(t *testing.T) {
	requireIsolatedTestEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads USERPROFILE on Windows
	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	tunnelCfg := filepath.Join(home, "cloudflared.yaml")
	for k, v := range map[string]any{
		"disable_caddy": false, "caddy_api": "http://127.0.0.1:1", "external_domain": "example.test",
		"cloudflare_tunnel_id": "tunnel-test", "cloudflare_tunnel_config": tunnelCfg, "cleanup_command": "touch " + filepath.Join(home, "cleanup-ran"),
	} {
		prev := viper.Get(k)
		viper.Set(k, v)
		t.Cleanup(func() { viper.Set(k, prev) })
	}
	_ = os.WriteFile(filepath.Join(cfgDir, "caddy-config.json"), []byte(`{"sentinel":true}`), 0o600)
	_ = os.WriteFile(tunnelCfg, []byte("sentinel: true\n"), 0o600)
	wt := filepath.Join(home, "wt-managed")
	_ = os.MkdirAll(wt, 0o700)
	now := time.Now()
	store := &session.SessionStore{Sessions: map[string]*session.Session{
		"managed": {Name: "managed", Branch: "managed", Path: wt, Ports: map[string]int{}, CreatedAt: now,
			LocalOnly: &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_x", CreatedAt: now}},
	}, NumberedSlots: map[int]string{}}
	if err := store.Overwrite(); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionByName("managed", removeSessionOptions{SkipConfirm: true, DiscardArtifacts: true, SyncRoutes: true}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(cfgDir, "caddy-config.json")); string(b) != `{"sentinel":true}` {
		t.Fatalf("caddy config rewritten: %s", b)
	}
	if b, _ := os.ReadFile(tunnelCfg); string(b) != "sentinel: true\n" {
		t.Fatalf("tunnel config rewritten: %s", b)
	}
	if _, err := os.Stat(filepath.Join(home, "cleanup-ran")); err == nil {
		t.Fatal("project cleanup command ran for a local-only session")
	}
	st, _ := session.LoadSessions()
	if _, ok := st.GetSession("managed"); ok {
		t.Fatal("session not removed")
	}
}

// `devx session create --reuse/--detach` must refuse a local-only session
// instead of re-creating it with ports/routes/template windows.
func TestSessionCreateRefusesLocalOnlySession(t *testing.T) {
	requireIsolatedTestEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads USERPROFILE on Windows
	_ = os.MkdirAll(filepath.Join(home, ".config", "devx"), 0o700)
	repo := filepath.Join(home, "repo")
	mustRun(t, "", "git", "init", "-q", "-b", "main", repo)
	mustRun(t, repo, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	reg := &config.ProjectRegistry{Projects: map[string]*config.Project{"proj": {Name: "proj", Path: repo}}}
	regData, _ := json.Marshal(reg)
	_ = os.WriteFile(filepath.Join(home, ".config", "devx", "projects.json"), regData, 0o600)
	now := time.Now()
	store := &session.SessionStore{Sessions: map[string]*session.Session{
		"managed": {Name: "managed", ProjectAlias: "proj", ProjectPath: repo, Branch: "managed", Path: filepath.Join(repo, ".worktrees", "managed"), Ports: map[string]int{}, CreatedAt: now,
			LocalOnly: &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_x", CreatedAt: now}},
	}, NumberedSlots: map[int]string{}}
	if err := store.Overwrite(); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []*bool{&reuseFlag, &detachFlag} {
		*flag = true
		projectFlag, noTmuxFlag = "proj", true
		err := runSessionCreate(sessionCreateCmd, []string{"managed"})
		*flag = false
		projectFlag, noTmuxFlag = "", false
		if err == nil || !strings.Contains(err.Error(), "local-only") {
			t.Fatalf("want local-only refusal, got %v", err)
		}
	}
	st, _ := session.LoadSessions()
	if s, _ := st.GetSession("managed"); !s.IsLocalOnly() || len(s.Ports) != 0 {
		t.Fatalf("record changed: %+v", s)
	}
}
