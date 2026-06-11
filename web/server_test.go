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
