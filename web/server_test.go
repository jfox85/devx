package web

import (
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
