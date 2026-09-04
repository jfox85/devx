package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfox85/devx/target"
	"github.com/jfox85/devx/usage"
	"github.com/spf13/viper"
)

// dashboardFixture is a two-provider Redline dashboard: claude exposes an
// account-scoped 5h window, codex only an account weekly plus a model pool, so
// both primary-window cases are covered.
const dashboardFixture = `{
  "generated_at": "2026-09-04T17:02:11Z",
  "providers": [
    {
      "id": "claude-main", "provider": "claude", "snapshot_stale": false,
      "snapshot": {
        "observed_at": "2026-09-04T16:31:23Z", "source": "openusage",
        "allowances": [
          {"key": "session", "source_label": "Session", "scope": "account", "role": "short", "remaining": 0.56, "resets_at": "2026-09-04T20:50:00Z", "period_duration_seconds": 18000},
          {"key": "weekly", "source_label": "Weekly", "scope": "account", "role": "weekly", "remaining": 0.15, "resets_at": "2026-09-04T16:59:59Z", "period_duration_seconds": 604800}
        ]
      }
    },
    {
      "id": "codex-main", "provider": "codex", "snapshot_stale": false,
      "snapshot": {
        "observed_at": "2026-09-04T16:31:20Z", "source": "native",
        "allowances": [
          {"key": "model:spark:short", "source_label": "Spark", "scope": "model", "role": "short", "remaining": 0.55, "resets_at": "2026-09-04T19:36:00Z", "period_duration_seconds": 18000},
          {"key": "weekly", "source_label": "Weekly", "scope": "account", "role": "weekly", "remaining": 0, "resets_at": "2026-09-07T03:03:02Z", "period_duration_seconds": 604800}
        ]
      }
    }
  ]
}`

// fakeRedline serves the dashboard and records provider refresh calls.
type fakeRedline struct {
	*httptest.Server
	mu          sync.Mutex
	refreshCode int
	refreshed   []string
}

func newFakeRedline(t *testing.T) *fakeRedline {
	t.Helper()
	f := &fakeRedline{refreshCode: http.StatusAccepted}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/dashboard" {
			w.Write([]byte(dashboardFixture)) //nolint:errcheck
			return
		}
		f.mu.Lock()
		f.refreshed = append(f.refreshed, r.URL.Path)
		code := f.refreshCode
		f.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeRedline) refreshedPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refreshed...)
}

func (f *fakeRedline) setRefreshCode(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCode = code
}

// newUsageServer returns a server whose poller talks to a fake Redline. The
// long poll interval keeps polling under the test's control.
func newUsageServer(t *testing.T) (*Server, *fakeRedline, *http.ServeMux) {
	t.Helper()
	fake := newFakeRedline(t)
	srv := newUsageTestServer(t)
	t.Setenv("REDLINE_API_TOKEN", "test-token")
	opts := UsageOptions{Enabled: true, PollInterval: time.Hour, RedlineURL: fake.URL, AllowRemote: true}
	if err := srv.ConfigureUsage(opts); err != nil {
		t.Fatalf("ConfigureUsage: %v", err)
	}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return srv, fake, mux
}

func decodeUsage(t *testing.T, body []byte) usageResponse {
	t.Helper()
	var out usageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode usage: %v (body %s)", err, body)
	}
	return out
}

type usageResponse struct {
	State     string `json:"state"`
	Providers []struct {
		ID      string `json:"id"`
		Primary *struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"primary"`
	} `json:"providers"`
}

func newUsageTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewWithBind("usage-test-token", 0, "", target.GatepostRuntimeConfig{})
	if err != nil {
		t.Fatalf("NewWithBind: %v", err)
	}
	return srv
}

func TestUsageDisabledServesDisabledStateAndRefuses(t *testing.T) {
	srv := newUsageTestServer(t)
	if err := srv.ConfigureUsage(UsageOptions{Enabled: false}); err != nil {
		t.Fatalf("ConfigureUsage: %v", err)
	}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/usage: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		State     string `json:"state"`
		Providers []any  `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode usage: %v (body %s)", err, w.Body.String())
	}
	if body.State != "disabled" {
		t.Fatalf("expected state disabled, got %q", body.State)
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/usage/refresh", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST /api/usage/refresh: expected 404 when disabled, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigureUsageToleratesMissingTokenButRejectsRemoteURL(t *testing.T) {
	srv := newUsageTestServer(t)
	t.Setenv("REDLINE_API_TOKEN", "")
	tokenFile := filepath.Join(t.TempDir(), "api-token")

	// A missing token must not stop the server booting: the UI reports the
	// rejected/not-running state instead.
	if err := srv.ConfigureUsage(UsageOptions{Enabled: true, TokenFile: tokenFile, RedlineURL: "http://127.0.0.1:7436"}); err != nil {
		t.Fatalf("missing token must not be a startup error, got %v", err)
	}
	if srv.usageOpts == nil {
		t.Fatal("expected usage to remain configured despite the missing token")
	}
	srv.startBackground()
	if srv.currentPoller() == nil {
		t.Fatal("expected a poller despite the missing token")
	}
	// ConfigureUsage must not be called again while the poller from the first
	// call is running (see its doc comment); stop it before testing the
	// separate non-loopback-URL rejection below.
	srv.stopBackground(context.Background())

	// A non-loopback URL without allow_remote would put the token on the network.
	err := srv.ConfigureUsage(UsageOptions{Enabled: true, TokenFile: tokenFile, RedlineURL: "http://redline.example.com:7436"})
	if err == nil {
		t.Fatal("expected a startup error for a non-loopback Redline URL without allow_remote")
	}
}

// TestConfigureUsageRejectsRemoteURLButServerStaysUp is the F4 regression
// test: cmd/web.go, cmd/web_windows.go and desktop/main.go must log a
// ConfigureUsage error and continue running with usage disabled rather than
// failing startup, so this asserts both halves of that contract at the
// Server level — ConfigureUsage returns an error for the bad URL, and
// GET /api/usage still serves state "disabled" afterward (the caller never
// having enabled usage).
func TestConfigureUsageRejectsRemoteURLButServerStaysUp(t *testing.T) {
	srv := newUsageTestServer(t)

	err := srv.ConfigureUsage(UsageOptions{Enabled: true, RedlineURL: "http://redline.example.com:7436"})
	if err == nil {
		t.Fatal("expected an error for a non-loopback Redline URL without allow_remote")
	}

	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/usage: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := decodeUsage(t, w.Body.Bytes())
	if got.State != "disabled" {
		t.Fatalf("expected state disabled after a rejected ConfigureUsage call, got %q: %s", got.State, w.Body.String())
	}
}

// TestConfigureUsageRejectsReconfigureWhileRunning is the F3 companion check:
// ConfigureUsage must refuse to replace usageOpts while a poller built from a
// previous call is still running, since usage.Poller.Start panics if called
// twice on the same instance and startBackground would otherwise build a
// second poller racing the first.
func TestConfigureUsageRejectsReconfigureWhileRunning(t *testing.T) {
	srv, _, _ := newUsageServer(t)
	srv.startBackground()
	defer srv.stopBackground(context.Background())

	if err := srv.ConfigureUsage(UsageOptions{Enabled: false}); err == nil {
		t.Fatal("expected ConfigureUsage to refuse reconfiguring a running poller")
	}
}

func TestUsageServesPolledProviders(t *testing.T) {
	srv, _, mux := newUsageServer(t)
	events := srv.hub.subscribe()
	defer srv.hub.unsubscribe(events)

	srv.startBackground()
	defer srv.stopBackground(context.Background())
	awaitUsageEvent(t, events)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/usage: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	got := decodeUsage(t, w.Body.Bytes())
	if got.State != "ok" {
		t.Fatalf("expected state ok, got %q", got.State)
	}
	if len(got.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d: %s", len(got.Providers), w.Body.String())
	}
	for _, p := range got.Providers {
		if p.Primary == nil {
			t.Fatalf("provider %q has no primary window: %s", p.ID, w.Body.String())
		}
	}
	if label := got.Providers[0].Primary.Label; label != "5h" {
		t.Errorf("claude primary label: expected 5h, got %q", label)
	}
	if label := got.Providers[1].Primary.Label; label != "wk" {
		t.Errorf("codex primary label: expected wk, got %q", label)
	}
}

func TestUsageBroadcastsOnlyWhenPayloadChanges(t *testing.T) {
	srv, _, _ := newUsageServer(t)
	events := srv.hub.subscribe()
	defer srv.hub.unsubscribe(events)

	srv.startBackground()
	defer srv.stopBackground(context.Background())
	awaitUsageEvent(t, events)

	// An identical second poll must not wake idle tabs.
	srv.usage.Poll(context.Background())
	select {
	case msg := <-events:
		t.Fatalf("unchanged poll broadcast an event: %q", msg)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUsageRefreshHitsEveryProviderThenThrottles(t *testing.T) {
	srv, fake, mux := newUsageServer(t)
	events := srv.hub.subscribe()
	defer srv.hub.unsubscribe(events)
	srv.startBackground()
	defer srv.stopBackground(context.Background())
	awaitUsageEvent(t, events)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/usage/refresh", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("POST /api/usage/refresh: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := decodeUsage(t, w.Body.Bytes()); len(got.Providers) != 2 {
		t.Fatalf("refresh response should carry usage, got %s", w.Body.String())
	}
	want := []string{"/v1/providers/claude-main/refresh", "/v1/providers/codex-main/refresh"}
	got := fake.refreshedPaths()
	for _, path := range want {
		if !containsString(got, path) {
			t.Fatalf("expected refresh of %s, got %v", path, got)
		}
	}

	// A second refresh inside the throttle window returns the cached usage.
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/usage/refresh", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("throttled refresh: expected 202, got %d: %s", w.Code, w.Body.String())
	}
	retry := w.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(retry)
	if err != nil || seconds <= 0 || seconds > 15 {
		t.Fatalf("expected Retry-After in 1..15 seconds, got %q (err %v)", retry, err)
	}
	if got := decodeUsage(t, w.Body.Bytes()); len(got.Providers) != 2 {
		t.Fatalf("throttled refresh should return cached usage, got %s", w.Body.String())
	}
}

func TestUsageRefreshReportsRejectedToken(t *testing.T) {
	srv, fake, mux := newUsageServer(t)
	events := srv.hub.subscribe()
	defer srv.hub.unsubscribe(events)
	srv.startBackground()
	defer srv.stopBackground(context.Background())
	awaitUsageEvent(t, events)

	fake.setRefreshCode(http.StatusUnauthorized)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/usage/refresh", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when Redline rejects the token, got %d: %s", w.Code, w.Body.String())
	}
	// The body must still be a plain usage.Usage, per the handler's documented
	// invariant: the SPA has one parser for this endpoint regardless of status.
	got := decodeUsage(t, w.Body.Bytes())
	if got.State != "unavailable" {
		t.Fatalf("expected state unavailable, got %q: %s", got.State, w.Body.String())
	}
	var full struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &full); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if full.Message != "Redline rejected the API token" {
		t.Fatalf("unexpected message: %#v (body %s)", full.Message, w.Body.String())
	}
}

func TestUsageShutdownStopsPoller(t *testing.T) {
	srv, _, _ := newUsageServer(t)
	srv.startBackground()
	done := srv.usageDone
	if done == nil {
		t.Fatal("startBackground did not start the poller")
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poller goroutine still running after Shutdown")
	}
}

// awaitUsageEvent waits for the first usage broadcast, which is also the signal
// that the initial poll completed.
func awaitUsageEvent(t *testing.T, events chan string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-events:
			if strings.HasPrefix(msg, "event: usage\n") {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for a usage SSE event")
		}
	}
}

// TestUsageOptionsFromGlobalConfigIgnoresProjectRedlineKeys is the F1
// regression test: a project-level .devx/config.yaml is repo-supplied and
// must never be able to redirect the Redline client (url), have it treat an
// arbitrary file as the bearer token (token_file), or allow a non-loopback
// host (allow_remote). Viper's merged singleton is what cmd/root.go's
// initConfig fills with exactly that project-over-global precedence, so this
// test seeds the singleton the same way a real project config would and
// asserts the redline.* fields still come from the global file/defaults.
func TestUsageOptionsFromGlobalConfigIgnoresProjectRedlineKeys(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A real global config, with legitimate redline settings.
	globalDir := filepath.Join(home, ".config", "devx")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	globalYAML := "usage:\n  redline:\n    url: http://127.0.0.1:9001\n    token_file: /tmp/global-token\n    allow_remote: false\n"
	if err := os.WriteFile(filepath.Join(globalDir, "config.yaml"), []byte(globalYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate what initConfig does for a project config: project values become
	// the effective viper.Get* result (SetDefault only fills gaps, so use Set to
	// mirror an actually-read project-level value, which wins over ReadInConfig
	// defaults from the global merge).
	viper.Set("usage.redline.url", "https://evil.example")
	viper.Set("usage.redline.allow_remote", true)
	viper.Set("usage.redline.token_file", "/tmp/x")

	opts := UsageOptionsFromGlobalConfig()

	if opts.RedlineURL != "http://127.0.0.1:9001" {
		t.Errorf("RedlineURL = %q, want the global-file value, not the project-supplied one", opts.RedlineURL)
	}
	if opts.TokenFile != "/tmp/global-token" {
		t.Errorf("TokenFile = %q, want the global-file value, not the project-supplied one", opts.TokenFile)
	}
	if opts.AllowRemote {
		t.Error("AllowRemote = true, want the global-file value (false), not the project-supplied one")
	}
}

// TestUsageOptionsFromGlobalConfigDefaultsRedlineFieldsWithNoGlobalFile covers
// the no-global-config case: all four fields must fall back to the documented
// defaults, not the zero value, even though a project config sets them.
func TestUsageOptionsFromGlobalConfigDefaultsRedlineFieldsWithNoGlobalFile(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", t.TempDir()) // no ~/.config/devx/config.yaml at all

	viper.Set("usage.redline.url", "https://evil.example")
	viper.Set("usage.redline.allow_remote", true)
	viper.Set("usage.redline.token_file", "/tmp/x")

	opts := UsageOptionsFromGlobalConfig()

	if opts.RedlineURL != usage.DefaultBaseURL {
		t.Errorf("RedlineURL = %q, want default %q", opts.RedlineURL, usage.DefaultBaseURL)
	}
	if opts.TokenFile != "" {
		t.Errorf("TokenFile = %q, want empty default", opts.TokenFile)
	}
	if opts.AllowRemote {
		t.Error("AllowRemote = true, want default false")
	}
	if !opts.Enabled {
		t.Error("Enabled = false, want default true")
	}
}

// TestUsageOptionsFromGlobalConfigHonorsEnvOverride confirms
// DEVX_USAGE_REDLINE_URL still works for the redline.* fields even though they
// bypass the merged viper singleton.
func TestUsageOptionsFromGlobalConfigHonorsEnvOverride(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEVX_USAGE_REDLINE_URL", "http://127.0.0.1:8123")

	opts := UsageOptionsFromGlobalConfig()

	if opts.RedlineURL != "http://127.0.0.1:8123" {
		t.Errorf("RedlineURL = %q, want the env override", opts.RedlineURL)
	}
}

// TestUsageStopStartReachesOKAgain is the F3 regression test: usage.Poller.Start
// panics on a second call to the same instance, so a Shutdown followed by a
// later Start (or PrivateServer.Serve) must not reuse it.
func TestUsageStopStartReachesOKAgain(t *testing.T) {
	srv, _, mux := newUsageServer(t)
	events := srv.hub.subscribe()
	defer srv.hub.unsubscribe(events)

	srv.startBackground()
	awaitUsageEvent(t, events)
	srv.stopBackground(context.Background())

	// Starting again after a stop must not panic and must reach state ok again.
	srv.startBackground()
	defer srv.stopBackground(context.Background())
	awaitUsageEvent(t, events)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/usage", nil))
	got := decodeUsage(t, w.Body.Bytes())
	if got.State != "ok" {
		t.Fatalf("expected state ok after stop\u2192start, got %q: %s", got.State, w.Body.String())
	}
}

// TestUsageRefreshRequiresAuth is an F5 regression test: web/usage.go's
// handlers must sit behind authMiddleware like every other /api/* route.
func TestUsageRefreshRequiresAuth(t *testing.T) {
	srv, _, mux := newUsageServer(t)
	handler := authMiddleware(srv.token, mux)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/usage/refresh", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated refresh: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUsageRefreshCookieAuthForeignOriginForbidden(t *testing.T) {
	srv, _, mux := newUsageServer(t)
	handler := authMiddleware(srv.token, mux)

	req := httptest.NewRequest("POST", "/api/usage/refresh", nil)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: srv.token})
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cookie-authenticated refresh with foreign Origin: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUsageBearerGetSucceeds(t *testing.T) {
	srv, _, mux := newUsageServer(t)
	handler := authMiddleware(srv.token, mux)

	req := httptest.NewRequest("GET", "/api/usage", nil)
	req.Header.Set("Authorization", "Bearer "+srv.token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bearer GET /api/usage: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
