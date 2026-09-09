package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientDashboardReadsBodyWithBearerToken(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"providers":[]}`))
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "secret"}
	body, err := c.Dashboard(context.Background())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}

	if got, want := string(body), `{"providers":[]}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if gotPath != "/v1/dashboard" {
		t.Errorf("path = %q, want %q", gotPath, "/v1/dashboard")
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
}

func TestClientDashboardOmitsAuthorizationWithoutToken(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := (&Client{BaseURL: srv.URL}).Dashboard(context.Background()); err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header sent without a token")
	}
}

func TestClientDashboardErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		wantErr    error
		wantSubstr string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: ErrUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, wantErr: ErrUnauthorized},
		{name: "server error", status: http.StatusInternalServerError, wantSubstr: "500"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			_, err := (&Client{BaseURL: srv.URL}).Dashboard(context.Background())
			if err == nil {
				t.Fatal("Dashboard = nil error, want an error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want errors.Is(%v)", err, tc.wantErr)
			}
			if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestClientRefreshPostsToTheProviderPath(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, Token: "secret"}
	if err := c.Refresh(context.Background(), "claude-main.1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/v1/providers/claude-main.1/refresh"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
}

func TestClientRefreshRejectsProviderIDsThatAreNotIdentifiers(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		wantErr    bool
	}{
		{name: "plain id", providerID: "claude-main"},
		{name: "dots and underscores", providerID: "claude_main.2"},
		{name: "path traversal", providerID: "../../v1/shutdown", wantErr: true},
		{name: "slash", providerID: "claude/main", wantErr: true},
		{name: "space", providerID: "claude main", wantErr: true},
		{name: "empty", providerID: "", wantErr: true},
		{name: "leading dash", providerID: "-claude", wantErr: true},
		{name: "too long", providerID: strings.Repeat("a", 65), wantErr: true},
		{name: "at the length limit", providerID: strings.Repeat("a", 64)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer srv.Close()

			err := (&Client{BaseURL: srv.URL, Token: "secret"}).Refresh(context.Background(), tc.providerID)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Refresh(%q): %v", tc.providerID, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Refresh(%q) = nil error, want it refused", tc.providerID)
			}
			if !strings.Contains(err.Error(), strconv.Quote(tc.providerID)) {
				t.Errorf("error = %v, want it to quote %q", err, tc.providerID)
			}
			if got := atomic.LoadInt32(&hits); got != 0 {
				t.Errorf("server received %d requests for %q, want 0", got, tc.providerID)
			}
		})
	}
}

func TestClientRefreshReportsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := (&Client{BaseURL: srv.URL}).Refresh(context.Background(), "claude-main")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want errors.Is(ErrUnauthorized)", err)
	}
}

func TestClientDashboardNeverFollowsARedirect(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		_, _ = w.Write([]byte(`{"providers":[]}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/dashboard", http.StatusFound)
	}))
	defer redirector.Close()

	_, err := (&Client{BaseURL: redirector.URL, Token: "secret"}).Dashboard(context.Background())
	if err == nil {
		t.Fatal("Dashboard = nil error, want a redirect to be refused")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %v, want it to name the redirect", err)
	}
	if got := atomic.LoadInt32(&targetHits); got != 0 {
		t.Errorf("redirect target received %d requests, want 0 so the bearer token is never resent", got)
	}
}

func TestClientRefreshNeverFollowsARedirect(t *testing.T) {
	var targetHits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	err := (&Client{BaseURL: redirector.URL, Token: "secret"}).Refresh(context.Background(), "claude-main")
	if err == nil {
		t.Fatal("Refresh = nil error, want a redirect to be refused")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %v, want it to name the redirect", err)
	}
	if got := atomic.LoadInt32(&targetHits); got != 0 {
		t.Errorf("redirect target received %d requests, want 0 so the bearer token is never resent", got)
	}
}

func TestNewClientRejectsNonLoopbackUnlessAllowed(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		allowRemote bool
		wantErr     bool
		wantBase    string
	}{
		{name: "empty defaults to loopback", wantBase: DefaultBaseURL},
		{name: "loopback ipv4", baseURL: "http://127.0.0.1:7436", wantBase: "http://127.0.0.1:7436"},
		{name: "loopback 127.x", baseURL: "http://127.9.9.9:7436", wantBase: "http://127.9.9.9:7436"},
		{name: "loopback ipv6", baseURL: "http://[::1]:7436", wantBase: "http://[::1]:7436"},
		{name: "localhost", baseURL: "http://localhost:7436", wantBase: "http://localhost:7436"},
		{name: "trailing slash trimmed", baseURL: "http://127.0.0.1:7436/", wantBase: "http://127.0.0.1:7436"},
		{name: "path stripped", baseURL: "http://127.0.0.1:7436/redline/v1", wantBase: "http://127.0.0.1:7436"},
		{name: "query refused", baseURL: "http://127.0.0.1:7436/?token=leak", wantErr: true},
		{name: "fragment refused", baseURL: "http://127.0.0.1:7436/#frag", wantErr: true},
		{name: "userinfo refused", baseURL: "http://user:hunter2@127.0.0.1:7436", wantErr: true},
		{name: "remote refused", baseURL: "http://redline.example.com:7436", wantErr: true},
		{name: "remote allowed over https", baseURL: "https://redline.example.com", allowRemote: true, wantBase: "https://redline.example.com"},
		{name: "remote over plain http refused", baseURL: "http://redline.example.com", allowRemote: true, wantErr: true},
		{name: "non http scheme", baseURL: "ftp://127.0.0.1:7436", wantErr: true},
		{name: "missing host", baseURL: "http://", wantErr: true},
		{name: "unparseable", baseURL: "http://[::1", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(tc.baseURL, "secret", tc.allowRemote)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NewClient(%q) = %+v, nil error; want an error", tc.baseURL, c)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewClient(%q): %v", tc.baseURL, err)
			}
			if c.BaseURL != tc.wantBase {
				t.Errorf("BaseURL = %q, want %q", c.BaseURL, tc.wantBase)
			}
			if c.Token != "secret" {
				t.Errorf("Token = %q, want %q", c.Token, "secret")
			}
		})
	}
}

func TestNewClientKeepsCredentialsOutOfItsErrors(t *testing.T) {
	_, err := NewClient("http://user:hunter2@redline.example.com", "secret", false)
	if err == nil {
		t.Fatal("NewClient = nil error, want a URL carrying credentials refused")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error = %v, want the password redacted", err)
	}
}

func TestClientDashboardRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[`))
		_, _ = w.Write(make([]byte, maxBodyBytes))
	}))
	defer srv.Close()

	_, err := (&Client{BaseURL: srv.URL}).Dashboard(context.Background())
	if err == nil {
		t.Fatal("Dashboard = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v, want it to say the response is too large", err)
	}
}

func TestDiscoverTokenUsesTheProcessEnvironment(t *testing.T) {
	t.Setenv("REDLINE_API_TOKEN", "  env-token ")

	token, path, err := DiscoverToken("")
	if err != nil {
		t.Fatalf("DiscoverToken: %v", err)
	}
	if token != "env-token" {
		t.Errorf("token = %q, want %q", token, "env-token")
	}
	if path != "" {
		t.Errorf("path = %q, want empty for an environment token", path)
	}
}

func TestDiscoverToken(t *testing.T) {
	home := t.TempDir()
	mustWriteToken := func(t *testing.T, path, token string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
		return path
	}

	darwinDefault := mustWriteToken(t, filepath.Join(home, "Library", "Application Support", "Redline", "api-token"), "  darwin-token\n")
	linuxDefault := mustWriteToken(t, filepath.Join(home, ".config", "redline", "api-token"), "linux-token\n")
	explicit := mustWriteToken(t, filepath.Join(t.TempDir(), "api-token"), "explicit-token\n")

	tests := []struct {
		name      string
		tokenFile string
		env       map[string]string
		goos      string
		want      string
		wantPath  string
	}{
		{name: "env wins", tokenFile: explicit, env: map[string]string{"REDLINE_API_TOKEN": " env-token "}, goos: "darwin", want: "env-token", wantPath: ""},
		{name: "explicit token file", tokenFile: explicit, goos: "darwin", want: "explicit-token", wantPath: explicit},
		{name: "darwin default", goos: "darwin", want: "darwin-token", wantPath: darwinDefault},
		{name: "linux default", goos: "linux", want: "linux-token", wantPath: linuxDefault},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string { return tc.env[key] }
			got, gotPath, err := discoverToken(tc.tokenFile, getenv, home, tc.goos)
			if err != nil {
				t.Fatalf("discoverToken: %v", err)
			}
			if got != tc.want {
				t.Errorf("token = %q, want %q", got, tc.want)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

func TestDiscoverTokenRefusesAnUnsafeTokenFile(t *testing.T) {
	t.Run("group or world readable", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "api-token")
		if err := os.WriteFile(path, []byte("leaky-token\n"), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod: %v", err)
		}

		token, _, err := discoverToken(path, func(string) string { return "" }, dir, "linux")
		if err == nil {
			t.Fatal("discoverToken = nil error, want a world-readable token file refused")
		}
		if token != "" {
			t.Errorf("token = %q, want it withheld", token)
		}
		if !strings.Contains(err.Error(), "readable by other users") {
			t.Errorf("error = %v, want it to explain the permissions", err)
		}
		if !strings.Contains(err.Error(), "0644") {
			t.Errorf("error = %v, want it to report mode 0644", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "real-token")
		if err := os.WriteFile(target, []byte("linked-token\n"), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
		link := filepath.Join(dir, "api-token")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}

		token, _, err := discoverToken(link, func(string) string { return "" }, dir, "linux")
		if err == nil {
			t.Fatal("discoverToken = nil error, want a symlinked token file refused")
		}
		if token != "" {
			t.Errorf("token = %q, want it withheld", token)
		}
	})

	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "api-token")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if _, _, err := discoverToken(path, func(string) string { return "" }, dir, "linux"); err == nil {
			t.Fatal("discoverToken = nil error, want a non-regular token file refused")
		}
	})
}

func TestDiscoverTokenReportsMissingPath(t *testing.T) {
	home := t.TempDir()
	getenv := func(string) string { return "" }

	_, _, err := discoverToken("", getenv, home, "darwin")
	if err == nil {
		t.Fatal("discoverToken = nil error, want an error")
	}
	wantPath := filepath.Join(home, "Library", "Application Support", "Redline", "api-token")
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error = %v, want it to mention %q", err, wantPath)
	}
}

func TestClientDashboardReportsUnreachableRedline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	_, err := (&Client{BaseURL: url}).Dashboard(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("error = %v, want errors.Is(ErrUnreachable)", err)
	}
}

func TestClientDashboardReportsTimeoutDistinctlyFromUnreachable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := (&Client{BaseURL: srv.URL}).Dashboard(ctx)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want errors.Is(ErrTimeout)", err)
	}
	if errors.Is(err, ErrUnreachable) {
		t.Errorf("error = %v, want a slow Redline not to be reported as unreachable", err)
	}
}

func TestClientRefreshReportsTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() { close(block); srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (&Client{BaseURL: srv.URL}).Refresh(ctx, "claude-main")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want errors.Is(ErrTimeout)", err)
	}
}
