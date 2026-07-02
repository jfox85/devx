package web

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/session"
	"github.com/spf13/viper"
)

func TestGetSessionsReturnsJSON(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
}

func TestGetSettingsReturnsArtifactTriggerKey(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/settings", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := resp["artifact_trigger_key"]; !ok {
		t.Fatalf("artifact_trigger_key missing from response: %#v", resp)
	}
}

func TestListProjectsReturnsProjectDefaultTargets(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	projectDir := filepath.Join(tmp, "nibit")
	if err := os.MkdirAll(filepath.Join(projectDir, ".devx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, ".devx", "config.yaml"), []byte("target: host\n"), 0644); err != nil {
		t.Fatal(err)
	}
	registryDir := filepath.Join(tmp, ".config", "devx")
	if err := os.MkdirAll(registryDir, 0755); err != nil {
		t.Fatal(err)
	}
	registryJSON := fmt.Sprintf(`{"projects":{"nibit":{"name":"nibit","path":%q},"mystorymates":{"name":"mystorymates","path":%q}}}`, projectDir, filepath.Join(tmp, "mystorymates"))
	if err := os.WriteFile(filepath.Join(registryDir, "projects.json"), []byte(registryJSON), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Projects []string          `json:"projects"`
		Targets  map[string]string `json:"targets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if got, want := resp.Projects, []string{"mystorymates", "nibit"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("projects = %v, want %v", got, want)
	}
	// handleListProjects resolves a concrete default target for every project so
	// the new-session form can always pre-select a type: the project's configured
	// target when set, otherwise the global default (here unset, so "host").
	if got := resp.Targets["nibit"]; got != "host" {
		t.Fatalf("nibit target = %q, want host; response=%s", got, w.Body.String())
	}
	if got := resp.Targets["mystorymates"]; got != "host" {
		t.Fatalf("mystorymates (no config) should fall back to global default host, got %q", got)
	}
}

func TestListProjectsRejectsInvalidProjectTarget(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	projectDir := filepath.Join(tmp, "bad")
	if err := os.MkdirAll(filepath.Join(projectDir, ".devx"), 0755); err != nil {
		t.Fatal(err)
	}
	// A project config with an unknown target must not leak into the UI defaults;
	// it should fall back to the global default (here unset, so "host").
	if err := os.WriteFile(filepath.Join(projectDir, ".devx", "config.yaml"), []byte("target: bogus\n"), 0644); err != nil {
		t.Fatal(err)
	}
	registryDir := filepath.Join(tmp, ".config", "devx")
	if err := os.MkdirAll(registryDir, 0755); err != nil {
		t.Fatal(err)
	}
	registryJSON := fmt.Sprintf(`{"projects":{"bad":{"name":"bad","path":%q}}}`, projectDir)
	if err := os.WriteFile(filepath.Join(registryDir, "projects.json"), []byte(registryJSON), 0644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Targets map[string]string `json:"targets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if got := resp.Targets["bad"]; got != "host" {
		t.Fatalf("invalid project target should fall back to host, got %q; response=%s", got, w.Body.String())
	}
}

func TestGetHealthReturnsOK(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestPaneCaptureTargetUsesExactMatchForSlashSessionNames(t *testing.T) {
	origRun := execTmuxRun
	execTmuxRun = func(args ...string) error { return nil }
	defer func() { execTmuxRun = origRun }()

	target, err := paneCaptureTarget("feature/foo", "2")
	if err != nil {
		t.Fatalf("paneCaptureTarget returned error: %v", err)
	}
	if want := "=feature/foo-web:.2"; target != want {
		t.Fatalf("expected %q, got %q", want, target)
	}
}

func TestPaneCaptureTargetRejectsNonNumericPane(t *testing.T) {
	if _, err := paneCaptureTarget("feature/foo", "abc"); err == nil {
		t.Fatal("expected error for non-numeric pane")
	}
}

func TestPaneCaptureLinesMatchesTmuxHistoryLimit(t *testing.T) {
	if paneCaptureLines != session.DefaultTmuxHistoryLimit {
		t.Fatalf("pane capture lines = %d, want %d", paneCaptureLines, session.DefaultTmuxHistoryLimit)
	}
}

func TestActivePaneReturnsPaneIndex(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return errors.New("no web session") },
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "display-message -t =jf-add-web: -p #{pane_index}"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("2\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/active-pane?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body activePaneResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body.Pane != 2 {
		t.Fatalf("expected pane 2, got %d", body.Pane)
	}
}

func TestPaneContentEndpointsRequireAuth(t *testing.T) {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	handler := authMiddleware("test-secret", mux)

	req := httptest.NewRequest("GET", "/api/pane-content/view?name=jf-add-web", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestPaneContentJSONReturnsCapturedContent(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error {
			if len(args) >= 1 && args[0] == "has-session" {
				return nil
			}
			return nil
		},
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "capture-pane -t =jf-add-web-web: -p -S -50000"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("line 1\nline 2\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/pane-content?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var body paneContentResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body.Content != "line 1\nline 2" {
		t.Fatalf("unexpected content %q", body.Content)
	}
	if body.Target != "=jf-add-web-web:" {
		t.Fatalf("unexpected target %q", body.Target)
	}
}

func TestPaneContentJSONRejectsInvalidPane(t *testing.T) {
	resp := authedRequest(t, "GET", "/api/pane-content?name=jf-add-web&pane=abc", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "pane must be a numeric index") {
		t.Fatalf("unexpected body: %s", resp.Body.String())
	}
}

func TestListWindowsUsesExactTargetForSlashSessionNames(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return errors.New("no web session") },
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "list-windows -t =feature/foo -F #{window_index}\t#{window_name}\t#{window_active}"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("1\teditor\t1\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/windows?name=feature%2Ffoo", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestSwitchWindowUsesExactTargetForSlashSessionNames(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error {
			got := strings.Join(args, " ")
			switch got {
			case "has-session -t =feature/foo-web":
				return errors.New("no web session")
			case "select-window -t =feature/foo:3":
				return nil
			default:
				t.Fatalf("unexpected tmux args: %q", got)
				return nil
			}
		},
		func(args ...string) ([]byte, error) { return nil, errors.New("unexpected output call") })

	resp := authedRequest(t, "POST", "/api/switch-window?name=feature%2Ffoo&window=3", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestRefreshUsesExactTargetForSlashSessionNames(t *testing.T) {
	seenRefresh := false
	seenListClients := false
	seenResize := false
	withStubbedTmux(t,
		func(args ...string) error {
			got := strings.Join(args, " ")
			switch got {
			case "has-session -t =feature/foo-web":
				return errors.New("no web session")
			case "refresh-client -t =feature/foo":
				seenRefresh = true
				return nil
			case "resize-window -t =feature/foo -x 120 -y 42":
				seenResize = true
				return nil
			default:
				t.Fatalf("unexpected tmux run args: %q", got)
				return nil
			}
		},
		func(args ...string) ([]byte, error) {
			got := strings.Join(args, " ")
			if got != "list-clients -t =feature/foo -F #{client_activity} #{client_width} #{client_height}" {
				t.Fatalf("unexpected tmux output args: %q", got)
			}
			seenListClients = true
			return []byte("123 120 42\n"), nil
		})

	resp := authedRequest(t, "POST", "/api/refresh?name=feature%2Ffoo", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
	if !(seenRefresh && seenListClients && seenResize) {
		t.Fatalf("expected refresh, list-clients, and resize-window to run; got refresh=%v list=%v resize=%v", seenRefresh, seenListClients, seenResize)
	}
}

func TestSendLiteralUsesExactTargetForSlashSessionNames(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error {
			got := strings.Join(args, " ")
			switch got {
			case "has-session -t =feature/foo-web":
				return errors.New("no web session")
			case "send-keys -t =feature/foo: -l -- hello world":
				return nil
			default:
				t.Fatalf("unexpected tmux args: %q", got)
				return nil
			}
		},
		func(args ...string) ([]byte, error) { return nil, errors.New("unexpected output call") })

	resp := authedRequest(t, "POST", "/api/send-keys?mode=literal&name=feature%2Ffoo&keys=hello%20world", nil)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestPaneContentTextReturnsPlainText(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return errors.New("no web session") },
		func(args ...string) ([]byte, error) {
			if got, want := strings.Join(args, " "), "capture-pane -t =jf-add-web: -p -S -50000"; got != want {
				t.Fatalf("unexpected tmux args: %q != %q", got, want)
			}
			return []byte("plain output\n"), nil
		})

	resp := authedRequest(t, "GET", "/api/pane-content.txt?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
	if body := resp.Body.String(); body != "plain output" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestPaneContentViewEscapesHTML(t *testing.T) {
	withStubbedTmux(t,
		func(args ...string) error { return nil },
		func(args ...string) ([]byte, error) {
			return []byte("<script>alert('xss')</script>\n& raw"), nil
		})

	resp := authedRequest(t, "GET", "/api/pane-content/view?name=jf-add-web", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected text/html content type, got %q", ct)
	}
	body := resp.Body.String()
	if strings.Contains(body, "<script>alert('xss')</script>") {
		t.Fatalf("response should escape pane content, got %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;") {
		t.Fatalf("expected escaped script tag in response, got %s", body)
	}
	if !strings.Contains(body, "requestAnimationFrame(scrollToBottom)") {
		t.Fatalf("expected auto-scroll script in html response")
	}
}

func TestPaneContentViewInvalidSessionReturnsPlainError(t *testing.T) {
	resp := authedRequest(t, "GET", "/api/pane-content/view?name=<bad>", nil)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if ct := resp.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
	if !strings.Contains(resp.Body.String(), "invalid session name") {
		t.Fatalf("unexpected body: %s", resp.Body.String())
	}
}

func withStubbedTmux(t *testing.T, run func(args ...string) error, output func(args ...string) ([]byte, error)) {
	t.Helper()
	origRun := execTmuxRun
	origOutput := execTmuxOutput
	execTmuxRun = run
	execTmuxOutput = output
	t.Cleanup(func() {
		execTmuxRun = origRun
		execTmuxOutput = origOutput
	})
}

func authedRequest(t *testing.T, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	handler := authMiddleware("test-secret", mux)
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestLoginCookieSecureFlag(t *testing.T) {
	prev := viper.GetString("web_secret_token")
	viper.Set("web_secret_token", "test-secret")
	t.Cleanup(func() { viper.Set("web_secret_token", prev) })

	cases := []struct {
		name       string
		remoteAddr string
		tls        bool
		xfp        string
		wantSecure bool
	}{
		{"direct plain HTTP localhost", "127.0.0.1:1234", false, "", false},
		{"direct TLS", "127.0.0.1:1234", true, "", true},
		{"trusted proxy forwarded HTTPS", "127.0.0.1:1234", false, "https", true},
		{"untrusted peer spoofing XFP", "203.0.113.7:1234", false, "https", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"token":"test-secret"}`))
			req.RemoteAddr = tc.remoteAddr
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			w := httptest.NewRecorder()
			handleLogin(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
			}
			var cookie *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == "devx_token" {
					cookie = c
				}
			}
			if cookie == nil {
				t.Fatal("devx_token cookie not set")
			}
			if cookie.Secure != tc.wantSecure {
				t.Errorf("cookie Secure = %v, want %v", cookie.Secure, tc.wantSecure)
			}
		})
	}
}

func TestGetSessionsIncludesStatusAndStaleSummary(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	worktree := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	store := &session.SessionStore{Sessions: map[string]*session.Session{
		"s1": {Name: "s1", Branch: "main", Path: worktree, CreatedAt: old, UpdatedAt: old},
	}, NumberedSlots: map[int]string{}}
	if err := store.Overwrite(); err != nil {
		t.Fatalf("overwrite sessions: %v", err)
	}

	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Sessions []struct {
			Name       string `json:"name"`
			TargetType string `json:"target_type"`
			Status     struct {
				Primary string `json:"primary"`
				Color   string `json:"color"`
			} `json:"status"`
			Stale struct {
				Category string `json:"category"`
			} `json:"stale"`
		} `json:"sessions"`
		StaleSummary struct {
			Total int `json:"total"`
		} `json:"stale_summary"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].Name != "s1" {
		t.Fatalf("unexpected sessions response: %#v", resp.Sessions)
	}
	if resp.Sessions[0].Status.Primary == "" || resp.Sessions[0].Status.Color == "" || resp.Sessions[0].Stale.Category == "" {
		t.Fatalf("missing status/stale fields: %#v", resp.Sessions[0])
	}
	if resp.Sessions[0].TargetType != "host" {
		t.Fatalf("target_type = %q, want host", resp.Sessions[0].TargetType)
	}
	if resp.StaleSummary.Total != 1 {
		t.Fatalf("stale summary total = %d, want 1", resp.StaleSummary.Total)
	}
}

func TestStaleEndpointsRejectInvalidDays(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	tooManyDays := strconv.Itoa(session.MaxStaleThresholdDays + 1)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{"GET", "/api/sessions/stale?days=abc"},
		{"GET", "/api/sessions/stale?days=" + tooManyDays},
		{"DELETE", "/api/sessions/stale-clean?days=0"},
		{"DELETE", "/api/sessions/stale-clean?days=" + tooManyDays},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: expected 400, got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestSetupEmptySessionStoreUsesIsolatedHome(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "devx")); err != nil {
		t.Fatalf("expected isolated devx config dir: %v", err)
	}
}

func TestSessionListCacheNoticesExternalMetadataMutation(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	worktree1 := t.TempDir()
	store := &session.SessionStore{Sessions: map[string]*session.Session{
		"s1": {Name: "s1", Branch: "main", Path: worktree1, CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}, NumberedSlots: map[int]string{}}
	if err := store.Overwrite(); err != nil {
		t.Fatalf("overwrite sessions: %v", err)
	}

	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	listNames := func() []string {
		req := httptest.NewRequest("GET", "/api/sessions", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Sessions []struct {
				Name string `json:"name"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		names := make([]string, 0, len(resp.Sessions))
		for _, s := range resp.Sessions {
			names = append(names, s.Name)
		}
		return names
	}
	if names := listNames(); len(names) != 1 || names[0] != "s1" {
		t.Fatalf("initial names = %v", names)
	}

	worktree2 := t.TempDir()
	store.Sessions["s2"] = &session.Session{Name: "s2", Branch: "main", Path: worktree2, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.Overwrite(); err != nil {
		t.Fatalf("overwrite sessions with s2: %v", err)
	}
	if names := listNames(); len(names) != 2 {
		t.Fatalf("cache returned stale sessions after metadata mutation: %v", names)
	}
}

func TestMarkSessionReviewedUsesExplicitReviewMarkerForStaleClassification(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lastAttached bool
	}{
		{name: "without-last-attached"},
		{name: "with-old-last-attached", lastAttached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupEmptySessionStoreForTest(t)
			old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
			sess := &session.Session{Name: "s1", Branch: "main", Path: t.TempDir(), CreatedAt: old, UpdatedAt: old}
			if tc.lastAttached {
				sess.LastAttached = old.Add(time.Hour)
			}
			store := &session.SessionStore{Sessions: map[string]*session.Session{"s1": sess}, NumberedSlots: map[int]string{}}
			if err := store.Overwrite(); err != nil {
				t.Fatalf("overwrite sessions: %v", err)
			}

			mux := http.NewServeMux()
			registerAPIRoutes(mux)
			req := httptest.NewRequest("POST", "/api/sessions/reviewed?name=s1", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			updatedStore, err := session.LoadSessions()
			if err != nil {
				t.Fatalf("load sessions: %v", err)
			}
			updated := updatedStore.Sessions["s1"]
			if !updated.LastReviewedAt.After(old) {
				t.Fatalf("LastReviewedAt = %v, want after %v", updated.LastReviewedAt, old)
			}

			req = httptest.NewRequest("GET", "/api/sessions/stale?days=1", nil)
			w = httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected stale scan 200, got %d: %s", w.Code, w.Body.String())
			}
			var resp struct {
				StaleSummary struct {
					Active   int `json:"active"`
					Statuses []struct {
						Category       string    `json:"category"`
						LastReviewedAt time.Time `json:"last_reviewed_at"`
						Reasons        []string  `json:"reasons"`
					} `json:"statuses"`
				} `json:"stale_summary"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			if resp.StaleSummary.Active != 1 || len(resp.StaleSummary.Statuses) != 1 || resp.StaleSummary.Statuses[0].Category != session.StaleCategoryActive {
				t.Fatalf("reviewed session should be active after stale scan: %#v", resp.StaleSummary)
			}
			if resp.StaleSummary.Statuses[0].LastReviewedAt.IsZero() {
				t.Fatalf("stale status did not expose last_reviewed_at: %#v", resp.StaleSummary.Statuses[0])
			}
		})
	}
}

func TestMarkSessionReviewedMapsPersistenceErrorsTo500(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	store := &session.SessionStore{Sessions: map[string]*session.Session{
		"s1": {Name: "s1", Branch: "main", Path: t.TempDir(), CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}, NumberedSlots: map[int]string{}}
	if err := store.Overwrite(); err != nil {
		t.Fatalf("overwrite sessions: %v", err)
	}

	orig := markSessionReviewed
	markSessionReviewed = func(string, time.Time) error { return errors.New("disk full") }
	defer func() { markSessionReviewed = orig }()

	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	req := httptest.NewRequest("POST", "/api/sessions/reviewed?name=s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for persistence error, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMarkSessionReviewedMapsMissingSessionTo404(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	orig := markSessionReviewed
	markSessionReviewed = func(string, time.Time) error { return fmt.Errorf("%w: missing", session.ErrSessionNotFound) }
	defer func() { markSessionReviewed = orig }()

	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	req := httptest.NewRequest("POST", "/api/sessions/reviewed?name=s1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing session, got %d: %s", w.Code, w.Body.String())
	}
}

func uploadImageRequest(t *testing.T, sessionName string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("image", "x.png")
	if err != nil {
		t.Fatal(err)
	}
	// Minimal valid PNG header so the handler's magic-byte sniff succeeds.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	png = append(png, make([]byte, 32)...)
	if _, err := part.Write(png); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("session", sessionName); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/upload-image", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestHandleUploadImageRejectsInvalidSession(t *testing.T) {
	for _, name := range []string{"../escape", "../../etc", "a/../b", "bad\x00name"} {
		w := httptest.NewRecorder()
		handleUploadImage(w, uploadImageRequest(t, name))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("session %q: expected 400, got %d: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestHandleUploadImageAcceptsValidSession(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	w := httptest.NewRecorder()
	handleUploadImage(w, uploadImageRequest(t, "my-session"))
	if w.Code == http.StatusBadRequest && strings.Contains(w.Body.String(), "invalid session") {
		t.Fatalf("valid session wrongly rejected: %s", w.Body.String())
	}
}
