package web

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewareRejectsUnauthorized(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No auth header
	req := httptest.NewRequest("GET", "/api/sessions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddlewareAcceptsBearerToken(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddlewareRejectsCookieUnsafeMethodWithoutOrigin(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "/api/sessions", nil)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cookie-authenticated POST without Origin, got %d", w.Code)
	}
}

func TestAuthMiddlewareRejectsCookieTerminalGetWithoutSameOriginProvenance(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://localhost:7777/terminal/demo/", nil)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for cookie-authenticated terminal GET without same-origin provenance, got %d", w.Code)
	}
}

func TestAuthMiddlewareAcceptsCookieTerminalWebSocketUpgradeWithoutFetchMetadata(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://localhost:7777/terminal/demo/ws", nil)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: token})
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for authenticated terminal websocket upgrade before upgrader origin enforcement, got %d", w.Code)
	}
}

func TestAuthMiddlewareAcceptsCookieTerminalGetWithSameOriginReferer(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://localhost:7777/terminal/demo/", nil)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: token})
	req.Header.Set("Referer", "http://localhost:7777/")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for same-origin terminal GET, got %d", w.Code)
	}
}

func TestAuthMiddlewareAcceptsCookieUnsafeMethodWithSameOrigin(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("POST", "http://localhost:7777/api/sessions", nil)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: token})
	req.Header.Set("Origin", "http://localhost:7777")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for same-origin cookie-authenticated POST, got %d", w.Code)
	}
}

func TestAuthMiddlewarePassesNonAPIRoutes(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Static assets don't require auth
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for non-API route, got %d", w.Code)
	}
}

func TestNewWithBindDefaultsToLoopback(t *testing.T) {
	srv, err := NewWithBind("test-secret", 0, "")
	if err != nil {
		t.Fatalf("NewWithBind returned error: %v", err)
	}
	if srv.bind != "127.0.0.1" {
		t.Fatalf("empty bind should default to loopback, got %q", srv.bind)
	}
}

func TestNewWithBindHonorsExplicitAddress(t *testing.T) {
	srv, err := NewWithBind("test-secret", 0, "0.0.0.0")
	if err != nil {
		t.Fatalf("NewWithBind returned error: %v", err)
	}
	if srv.bind != "0.0.0.0" {
		t.Fatalf("explicit bind not honored, got %q", srv.bind)
	}
}

func TestNewDefaultsToLoopbackBind(t *testing.T) {
	srv, err := New("test-secret", 0)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if srv.bind != "127.0.0.1" {
		t.Fatalf("New should default to loopback bind, got %q", srv.bind)
	}
}

func TestEffectiveHostsIncludesForwardedHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.RemoteAddr = "127.0.0.1:54321"

	hosts := effectiveHosts(req)
	if len(hosts) != 2 || hosts[0] != "localhost" || hosts[1] != "devx-demo-web.example.com" {
		t.Fatalf("unexpected effectiveHosts: %#v", hosts)
	}
}

func TestEffectiveHostsTakesFirstForwardedHostValue(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", " devx-demo-web.example.com , internal")
	req.RemoteAddr = "127.0.0.1:54321"

	hosts := effectiveHosts(req)
	if len(hosts) != 2 || hosts[1] != "devx-demo-web.example.com" {
		t.Fatalf("expected first forwarded host trimmed, got %#v", hosts)
	}
}

func TestOriginMatchesHostHonorsForwardedHost(t *testing.T) {
	// Behind Caddy/CF the upstream Host is rewritten to localhost while the
	// browser's Origin carries the real external host (forwarded as XFH).
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Origin", "https://devx-demo-web.example.com")

	if !originMatchesHost(req) {
		t.Fatal("expected forwarded-host Origin to match")
	}
}

func TestEffectiveHostsIgnoresForwardedHostFromUntrustedPeer(t *testing.T) {
	// Forwarded headers are only honored when the direct peer is a trusted
	// proxy (loopback/private). A request from a public address — e.g. the
	// backend bound to 0.0.0.0 and reached directly — must not be able to
	// spoof X-Forwarded-Host to widen the same-origin check.
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.RemoteAddr = "203.0.113.7:44321" // public (TEST-NET-3)
	req.Header.Set("X-Forwarded-Host", "attacker.example.com")
	req.Header.Set("Origin", "https://attacker.example.com")

	hosts := effectiveHosts(req)
	if len(hosts) != 1 || hosts[0] != "localhost" {
		t.Fatalf("untrusted peer must not extend effectiveHosts, got %#v", hosts)
	}
	if originMatchesHost(req) {
		t.Fatal("spoofed forwarded-host Origin must not match from an untrusted peer")
	}
}

func TestEffectiveHostsIgnoresForwardedHostFromPrivateUntrustedPeer(t *testing.T) {
	// Default trust is loopback-only: a private-range peer (LAN host, VPN,
	// container bridge) is NOT trusted unless web_trusted_proxies opts in.
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.RemoteAddr = "192.168.1.50:44321"
	req.Header.Set("X-Forwarded-Host", "attacker.example.com")

	hosts := effectiveHosts(req)
	if len(hosts) != 1 || hosts[0] != "localhost" {
		t.Fatalf("private untrusted peer must not extend effectiveHosts, got %#v", hosts)
	}
}

func TestConfigureTrustedProxiesExtendsTrust(t *testing.T) {
	old := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = old })

	if err := configureTrustedProxies([]string{"172.16.0.0/12"}); err != nil {
		t.Fatalf("configureTrustedProxies: %v", err)
	}

	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.RemoteAddr = "172.17.0.1:44321" // docker bridge gateway, inside 172.16/12
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")

	hosts := effectiveHosts(req)
	if len(hosts) != 2 || hosts[1] != "devx-demo-web.example.com" {
		t.Fatalf("configured proxy CIDR should be trusted, got %#v", hosts)
	}

	// Loopback must still be trusted alongside the configured CIDR.
	req.RemoteAddr = "127.0.0.1:44321"
	if hosts := effectiveHosts(req); len(hosts) != 2 {
		t.Fatalf("loopback should remain trusted after configuration, got %#v", hosts)
	}

	// A peer outside loopback + configured CIDRs stays untrusted.
	req.RemoteAddr = "192.168.1.50:44321"
	if hosts := effectiveHosts(req); len(hosts) != 1 {
		t.Fatalf("unconfigured private peer must stay untrusted, got %#v", hosts)
	}
}

func TestConfigureTrustedProxiesRejectsInvalidCIDR(t *testing.T) {
	old := trustedProxyNets
	t.Cleanup(func() { trustedProxyNets = old })

	if err := configureTrustedProxies([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestRequestIsHTTPS(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		tls        bool
		xfp        string
		want       bool
	}{
		{"direct plain HTTP", "127.0.0.1:1234", false, "", false},
		{"direct TLS", "127.0.0.1:1234", true, "", true},
		{"trusted proxy XFP https", "127.0.0.1:1234", false, "https", true},
		{"trusted proxy XFP https list", "127.0.0.1:1234", false, "https, http", true},
		{"trusted proxy XFP http", "127.0.0.1:1234", false, "http", false},
		{"untrusted peer XFP https spoof", "203.0.113.7:1234", false, "https", false},
		{"private untrusted peer XFP https spoof", "192.168.1.50:1234", false, "https", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/login", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			if got := requestIsHTTPS(req); got != tc.want {
				t.Errorf("requestIsHTTPS = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOriginMatchesHostRejectsForeignOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Origin", "https://evil.example.com")

	if originMatchesHost(req) {
		t.Fatal("foreign Origin must not match even with a forwarded host present")
	}
}

func TestAuthMiddlewareAcceptsCookieTerminalGetViaForwardedHostOrigin(t *testing.T) {
	token := "test-secret"
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Simulate a terminal asset GET arriving through Caddy/CF: Host rewritten to
	// localhost, real host in X-Forwarded-Host, browser Origin = real host.
	req := httptest.NewRequest("GET", "/terminal/demo/", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Origin", "https://devx-demo-web.example.com")
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for forwarded-host same-origin terminal GET, got %d", w.Code)
	}
}

func TestUpgraderCheckOriginHonorsForwardedHost(t *testing.T) {
	req := httptest.NewRequest("GET", "/terminal/demo/ws", nil)
	req.Host = "localhost"
	req.Header.Set("X-Forwarded-Host", "devx-demo-web.example.com")
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Origin", "https://devx-demo-web.example.com")

	if !upgrader.CheckOrigin(req) {
		t.Fatal("WebSocket CheckOrigin should accept forwarded-host Origin")
	}

	req.Header.Set("Origin", "https://evil.example.com")
	if upgrader.CheckOrigin(req) {
		t.Fatal("WebSocket CheckOrigin must reject a foreign Origin")
	}
}
