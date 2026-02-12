package caddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestRouteGeneration(t *testing.T) {
	route := Route{
		ID: "sess-feat-ui-ui",
		Match: []RouteMatch{
			{
				Host: []string{"feat-ui-ui.localhost"},
			},
		},
		Handle: []RouteHandler{
			{
				Handler: "reverse_proxy",
				Upstreams: []RouteUpstream{
					{
						Dial: "127.0.0.1:3000",
					},
				},
			},
		},
		Terminal: true,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("failed to marshal route: %v", err)
	}

	// Verify key fields are present
	jsonStr := string(jsonData)
	expectedFields := []string{
		`"@id":"sess-feat-ui-ui"`,
		`"host":["feat-ui-ui.localhost"]`,
		`"handler":"reverse_proxy"`,
		`"dial":"127.0.0.1:3000"`,
		`"terminal":true`,
	}

	for _, field := range expectedFields {
		if !contains(jsonStr, field) {
			t.Errorf("expected field %s not found in JSON: %s", field, jsonStr)
		}
	}
}

func TestGetServiceMapping(t *testing.T) {
	tests := []struct {
		portName string
		expected string
	}{
		{"FE_PORT", "ui"},
		{"WEB_PORT", "ui"},
		{"FRONTEND", "ui"},
		{"API_PORT", "api"},
		{"BACKEND", "api"},
		{"DB_PORT", "db"},
		{"DATABASE", "db"},
		{"REDIS_PORT", "redis"},
		{"AUTH_SERVICE_PORT", "auth-service"},
		{"PAYMENT_PORT", "payment"},
		{"CUSTOM_THING_PORT", "custom-thing"},
	}

	for _, test := range tests {
		result := GetServiceMapping(test.portName)
		if result != test.expected {
			t.Errorf("GetServiceMapping(%s) = %s, expected %s",
				test.portName, result, test.expected)
		}
	}
}

func TestSanitizeHostname(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic cases
		{"simple", "simple"},
		{"Simple-Case", "simple-case"},
		{"UPPERCASE", "uppercase"},

		// Slash handling (the main issue)
		{"codex/add-ui-for-prompt-presets-in-frontend", "codex-add-ui-for-prompt-presets-in-frontend"},
		{"feature/user-auth", "feature-user-auth"},
		{"fix/api/endpoint", "fix-api-endpoint"},

		// Multiple slashes
		{"deep/nested/branch/name", "deep-nested-branch-name"},

		// Underscore handling
		{"branch_with_underscores", "branch-with-underscores"},
		{"mix_of/slash_and_underscore", "mix-of-slash-and-underscore"},

		// Special characters
		{"branch.with.dots", "branch-with-dots"},
		{"branch@with#special$chars", "branch-with-special-chars"},
		{"branch with spaces", "branch-with-spaces"},

		// Edge cases
		{"", ""},
		{"---multiple---hyphens---", "multiple-hyphens"},
		{"-leading-and-trailing-", "leading-and-trailing"},
		{"123-numeric-456", "123-numeric-456"},

		// Complex real-world examples
		{"feature/auth/oauth2-integration", "feature-auth-oauth2-integration"},
		{"hotfix/payment_processing/stripe_api", "hotfix-payment-processing-stripe-api"},
	}

	for _, test := range tests {
		result := SanitizeHostname(test.input)
		if result != test.expected {
			t.Errorf("SanitizeHostname(%q) = %q, expected %q",
				test.input, result, test.expected)
		}
	}
}

func TestNormalizeDNSName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"UPPERCASE", "uppercase"},
		{"with_underscores", "with-underscores"},
		{"with spaces", "with-spaces"},
		{"with@special#chars", "with-special-chars"},
		{"", ""},
		{"---multiple---hyphens---", "multiple-hyphens"},
		{"-leading-and-trailing-", "leading-and-trailing"},
	}

	for _, test := range tests {
		result := NormalizeDNSName(test.input)
		if result != test.expected {
			t.Errorf("NormalizeDNSName(%q) = %q, expected %q",
				test.input, result, test.expected)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// newTestClient creates a CaddyClient wired to the given httptest.Server,
// bypassing NewCaddyClient (which uses viper and does live discovery).
func newTestClient(ts *httptest.Server, serverName string) *CaddyClient {
	client := resty.New()
	client.SetTimeout(5 * time.Second)
	return &CaddyClient{
		client:     client,
		baseURL:    ts.URL,
		serverName: serverName,
	}
}

// --- discoverServerName tests ---

func TestDiscoverServerName(t *testing.T) {
	t.Run("finds srv1 with :80", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"srv0": map[string]any{"listen": []string{":443"}},
				"srv1": map[string]any{"listen": []string{":80"}},
			})
		}))
		defer ts.Close()

		c := newTestClient(ts, "placeholder")
		c.discoverServerName()
		if c.serverName != "srv1" {
			t.Errorf("expected srv1, got %s", c.serverName)
		}
	})

	t.Run("finds srv0 with :80", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"srv0": map[string]any{"listen": []string{":80"}},
				"srv1": map[string]any{"listen": []string{":443"}},
			})
		}))
		defer ts.Close()

		c := newTestClient(ts, "placeholder")
		c.discoverServerName()
		if c.serverName != "srv0" {
			t.Errorf("expected srv0, got %s", c.serverName)
		}
	})

	t.Run("falls back to srv1 when no :80 server", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"myserver": map[string]any{"listen": []string{":443"}},
			})
		}))
		defer ts.Close()

		c := newTestClient(ts, "placeholder")
		c.discoverServerName()
		if c.serverName != "srv1" {
			t.Errorf("expected fallback srv1, got %s", c.serverName)
		}
	})

	t.Run("falls back to srv1 on API error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		c := newTestClient(ts, "placeholder")
		c.discoverServerName()
		if c.serverName != "srv1" {
			t.Errorf("expected fallback srv1, got %s", c.serverName)
		}
	})
}

// --- EnsureRoutesArray tests ---

func TestEnsureRoutesArray(t *testing.T) {
	t.Run("routes already exists — no PATCH", func(t *testing.T) {
		patched := false
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"listen":[":80"],"routes":[]}`))
				return
			}
			if r.Method == http.MethodPatch {
				patched = true
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		c := newTestClient(ts, "srv1")
		if err := c.EnsureRoutesArray(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if patched {
			t.Error("expected no PATCH when routes already exists")
		}
	})

	t.Run("routes is null — sends PATCH", func(t *testing.T) {
		patched := false
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"listen":[":80"],"routes":null}`))
				return
			}
			if r.Method == http.MethodPatch {
				patched = true
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		c := newTestClient(ts, "srv1")
		if err := c.EnsureRoutesArray(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !patched {
			t.Error("expected PATCH when routes is null")
		}
	})

	t.Run("routes key missing — sends PATCH", func(t *testing.T) {
		patched := false
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"listen":[":80"]}`))
				return
			}
			if r.Method == http.MethodPatch {
				patched = true
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		c := newTestClient(ts, "srv1")
		if err := c.EnsureRoutesArray(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !patched {
			t.Error("expected PATCH when routes key is missing")
		}
	})
}

// --- GetAllRoutes null/404 handling tests ---

func TestGetAllRoutesNullResponse(t *testing.T) {
	t.Run("null body returns empty slice", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("null"))
		}))
		defer ts.Close()

		c := newTestClient(ts, "srv1")
		routes, err := c.GetAllRoutes()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(routes) != 0 {
			t.Errorf("expected empty slice, got %d routes", len(routes))
		}
	})

	t.Run("404 returns empty slice", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		c := newTestClient(ts, "srv1")
		routes, err := c.GetAllRoutes()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(routes) != 0 {
			t.Errorf("expected empty slice, got %d routes", len(routes))
		}
	})

	t.Run("valid routes are parsed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]Route{
				{ID: "test-route"},
			})
		}))
		defer ts.Close()

		c := newTestClient(ts, "srv1")
		routes, err := c.GetAllRoutes()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(routes) != 1 || routes[0].ID != "test-route" {
			t.Errorf("expected 1 route with ID test-route, got %v", routes)
		}
	})
}

// --- serverPath tests ---

func TestServerPath(t *testing.T) {
	c := &CaddyClient{serverName: "myserver"}
	expected := "/config/apps/http/servers/myserver"
	if got := c.serverPath(); got != expected {
		t.Errorf("serverPath() = %q, want %q", got, expected)
	}
}

// --- Integration: routes use discovered server path ---

func TestRoutesUseDiscoveredServer(t *testing.T) {
	var mu sync.Mutex
	var requestPaths []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestPaths = append(requestPaths, r.URL.Path)
		mu.Unlock()

		// Discovery endpoint
		if r.URL.Path == "/config/apps/http/servers" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"myhttp": map[string]any{"listen": []string{":80"}},
			})
			return
		}

		// Routes GET
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("[]"))
			return
		}

		// Routes POST
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client := resty.New()
	client.SetTimeout(5 * time.Second)
	c := &CaddyClient{
		client:  client,
		baseURL: ts.URL,
	}
	c.discoverServerName()

	if c.serverName != "myhttp" {
		t.Fatalf("expected myhttp, got %s", c.serverName)
	}

	// GetAllRoutes should use /config/apps/http/servers/myhttp/routes
	_, _ = c.GetAllRoutes()

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, p := range requestPaths {
		if p == "/config/apps/http/servers/myhttp/routes" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected request to /config/apps/http/servers/myhttp/routes, got paths: %v", requestPaths)
	}
}
