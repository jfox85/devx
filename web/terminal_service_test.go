package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/target"
)

func newTestWebServer(t *testing.T) *Server {
	t.Helper()
	s, err := New("test-secret", 0, target.GatepostRuntimeConfig{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return s
}

func serveServerRequest(t *testing.T, s *Server, method, path string, body *bytes.Buffer, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	handler := authMiddleware("test-secret", mux)
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body.Bytes())
	}
	req := httptest.NewRequest(method, path, reader)
	if authed {
		req.Header.Set("Authorization", "Bearer test-secret")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestTerminalStatusRequiresAuth(t *testing.T) {
	s := newTestWebServer(t)
	resp := serveServerRequest(t, s, "GET", "/api/terminal/status?session=demo", nil, false)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestTerminalStatusReturnsRedactedState(t *testing.T) {
	s := newTestWebServer(t)
	s.terminal.loadStore = func() (*session.SessionStore, error) {
		return &session.SessionStore{Sessions: map[string]*session.Session{
			"demo": {Name: "demo", Path: t.TempDir()},
		}}, nil
	}
	if _, err := s.ttyd.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatalf("startForSession returned error: %v", err)
	}
	t.Cleanup(func() { s.ttyd.stopSession("demo") })

	resp := serveServerRequest(t, s, "GET", "/api/terminal/status?session=demo", nil, true)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, forbidden := range []string{"port", "pid", "path", "command"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("status leaked %q: %#v", forbidden, body)
		}
	}
	if body["state"] != string(terminalStateReady) || body["ready"] != true || body["running"] != true {
		t.Fatalf("unexpected status body: %#v", body)
	}
}

func TestTerminalPrewarmRejectsCrossOrigin(t *testing.T) {
	s := newTestWebServer(t)
	body := bytes.NewBufferString(`{"session":"demo"}`)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	handler := authMiddleware("test-secret", mux)
	req := httptest.NewRequest("POST", "/api/terminal/prewarm", body)
	req.Header.Set("Authorization", "Bearer test-secret")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTerminalPrewarmRejectsCookieAuthWithoutOrigin(t *testing.T) {
	s := newTestWebServer(t)
	body := bytes.NewBufferString(`{"session":"demo"}`)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	handler := authMiddleware("test-secret", mux)
	req := httptest.NewRequest("POST", "/api/terminal/prewarm", body)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: "test-secret"})
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTerminalPrewarmAcceptsForwardedHostOrigin(t *testing.T) {
	// devx behind Caddy/Cloudflare tunnel: upstream Host is rewritten to
	// localhost but the browser Origin is the external hostname, carried in
	// X-Forwarded-Host. The write guard must not reject these as cross-origin.
	s := newTestWebServer(t)
	body := bytes.NewBufferString(`{"session":"demo"}`)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	handler := authMiddleware("test-secret", mux)
	req := httptest.NewRequest("POST", "http://localhost:7777/api/terminal/prewarm", body)
	req.AddCookie(&http.Cookie{Name: "devx_token", Value: "test-secret"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://devx.example.com")
	req.Header.Set("X-Forwarded-Host", "devx.example.com")
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Fatalf("expected forwarded-host origin to pass the write guard, got 403: %s", w.Body.String())
	}
}

func TestTerminalSendInputUsesNamedBufferAndActivePaneTarget(t *testing.T) {
	s := newTestWebServer(t)
	s.terminal.loadStore = func() (*session.SessionStore, error) {
		return &session.SessionStore{Sessions: map[string]*session.Session{
			"demo": {Name: "demo", Path: t.TempDir()},
		}}, nil
	}
	withStubbedTmux(t,
		func(args ...string) error {
			if strings.Join(args, " ") == "has-session -t =demo-web" {
				return errors.New("no web session")
			}
			t.Fatalf("unexpected tmux run args: %q", strings.Join(args, " "))
			return nil
		},
		func(args ...string) ([]byte, error) { return nil, errors.New("unexpected tmux output") })

	var gotBuffer, gotTarget, gotText string
	var gotSubmit bool
	s.terminal.tmuxInput = func(bufferName, target, text string, submit bool) error {
		gotBuffer, gotTarget, gotText, gotSubmit = bufferName, target, text, submit
		return nil
	}

	body := bytes.NewBufferString(`{"session":"demo","text":"hello world","submit":true,"mode":"paste-buffer"}`)
	resp := serveServerRequest(t, s, "POST", "/api/terminal/send-input", body, true)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.HasPrefix(gotBuffer, "devx-") {
		t.Fatalf("expected devx buffer name, got %q", gotBuffer)
	}
	if gotTarget != "=demo:" {
		t.Fatalf("expected active pane target =demo:, got %q", gotTarget)
	}
	if gotText != "hello world" || !gotSubmit {
		t.Fatalf("unexpected input: text=%q submit=%v", gotText, gotSubmit)
	}
}

func TestTerminalSendInputRejectsOversizedText(t *testing.T) {
	s := newTestWebServer(t)
	s.terminal.loadStore = func() (*session.SessionStore, error) {
		return &session.SessionStore{Sessions: map[string]*session.Session{
			"demo": {Name: "demo", Path: t.TempDir()},
		}}, nil
	}
	large := strings.Repeat("x", terminalSendInputMaxBytes+1)
	payload, _ := json.Marshal(map[string]any{"session": "demo", "text": large, "mode": "paste-buffer"})
	resp := serveServerRequest(t, s, "POST", "/api/terminal/send-input", bytes.NewBuffer(payload), true)
	if resp.Code != http.StatusRequestEntityTooLarge && resp.Code != http.StatusBadRequest {
		t.Fatalf("expected payload rejection, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestTtydPrewarmCountAndIdleCleanup(t *testing.T) {
	m := newTtydManager()
	if _, err := m.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatalf("startForSession returned error: %v", err)
	}
	m.markPrewarmed("demo", 20*time.Millisecond)
	if got := m.prewarmedCount(); got != 1 {
		t.Fatalf("expected one prewarmed session, got %d", got)
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := m.statusForSession("demo"); ok {
		t.Fatal("expected prewarmed session to be cleaned up after idle timeout")
	}
}
