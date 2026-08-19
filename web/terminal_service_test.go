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

func TestTerminalStatusDoesNotExposeConnectionProof(t *testing.T) {
	s := newTestWebServer(t)
	s.terminal.loadStore = func() (*session.SessionStore, error) {
		return &session.SessionStore{Sessions: map[string]*session.Session{
			"demo": {Name: "demo", Path: t.TempDir()},
		}}, nil
	}
	if _, err := s.ttyd.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ttyd.stopSession("demo") })
	s.ttyd.clientConnected("demo", "attempt-private-1234")

	resp := serveServerRequest(t, s, http.MethodGet, "/api/terminal/status?session=demo", nil, true)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"connection_id", "attempt", "receipt"} {
		if _, exists := body[forbidden]; exists {
			t.Fatalf("status leaked %s: %#v", forbidden, body)
		}
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

func TestTerminalSendInputLiteralUsesSendKeysWithoutBuffer(t *testing.T) {
	s := newTestWebServer(t)
	s.terminal.loadStore = func() (*session.SessionStore, error) {
		return &session.SessionStore{Sessions: map[string]*session.Session{
			"demo": {Name: "demo", Path: t.TempDir()},
		}}, nil
	}
	var calls []string
	withStubbedTmux(t,
		func(args ...string) error {
			call := strings.Join(args, " ")
			if call == "has-session -t =demo-web" {
				return errors.New("no web session")
			}
			calls = append(calls, call)
			return nil
		},
		func(args ...string) ([]byte, error) { return nil, errors.New("unexpected tmux output") })

	body := bytes.NewBufferString(`{"session":"demo","text":"abc","submit":true,"mode":"literal"}`)
	resp := serveServerRequest(t, s, "POST", "/api/terminal/send-input", body, true)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", resp.Code, resp.Body.String())
	}
	want := []string{"send-keys -t =demo: -l -- abc", "send-keys -t =demo: Enter"}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected tmux calls:\n%s", strings.Join(calls, "\n"))
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

func TestTerminalActivityRequiresLiveConnectionAndRecordsAttach(t *testing.T) {
	setupEmptySessionStoreForTest(t)
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddSession("demo", "main", t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	s := newTestWebServer(t)
	if _, err := s.ttyd.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ttyd.stopSession("demo") })

	issueReceipt := func(sessionName, attempt string) (string, *httptest.ResponseRecorder) {
		payload, _ := json.Marshal(map[string]any{"session": sessionName, "attempt": attempt})
		resp := serveServerRequest(t, s, http.MethodPost, "/api/terminal/activity-receipt", bytes.NewBuffer(payload), true)
		var body struct {
			Receipt string `json:"receipt"`
		}
		_ = json.Unmarshal(resp.Body.Bytes(), &body)
		return body.Receipt, resp
	}
	postActivity := func(receipt string) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]any{"session": "demo", "receipt": receipt})
		return serveServerRequest(t, s, http.MethodPost, "/api/sessions/activity", bytes.NewBuffer(payload), true)
	}
	if _, resp := issueReceipt("demo", "not-connected"); resp.Code != http.StatusConflict {
		t.Fatalf("inactive attempt status = %d, want 409: %s", resp.Code, resp.Body.String())
	}
	s.ttyd.clientConnected("demo", "attempt-a-12345678")
	if _, err := s.ttyd.startForSession("other", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.ttyd.stopSession("other") })
	s.ttyd.clientConnected("other", "attempt-b-12345678")
	if _, resp := issueReceipt("demo", "attempt-b-12345678"); resp.Code != http.StatusConflict {
		t.Fatalf("cross-session attempt status = %d, want 409: %s", resp.Code, resp.Body.String())
	}
	receipt, resp := issueReceipt("demo", "attempt-a-12345678")
	if resp.Code != http.StatusOK || receipt == "" {
		t.Fatalf("active attempt receipt status = %d receipt=%q: %s", resp.Code, receipt, resp.Body.String())
	}
	if resp := postActivity(receipt); resp.Code != http.StatusNoContent {
		t.Fatalf("active receipt status = %d, want 204: %s", resp.Code, resp.Body.String())
	}
	if resp := postActivity(receipt); resp.Code != http.StatusConflict {
		t.Fatalf("replayed receipt status = %d, want 409: %s", resp.Code, resp.Body.String())
	}
	reloaded, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Sessions["demo"].LastAttached.IsZero() {
		t.Fatal("active terminal connection did not record activity")
	}

	s.ttyd.clientDisconnected("demo", "attempt-a-12345678")
	if _, resp := issueReceipt("demo", "attempt-a-12345678"); resp.Code != http.StatusConflict {
		t.Fatalf("disconnected attempt status = %d, want 409: %s", resp.Code, resp.Body.String())
	}
}

func TestTerminalAttemptRemainsActiveAcrossOverlappingReconnect(t *testing.T) {
	m := newTtydManager()
	if _, err := m.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopSession("demo") })
	attempt := "attempt-overlap-1234"
	m.clientConnected("demo", attempt)
	m.clientConnected("demo", attempt)
	m.clientDisconnected("demo", attempt)
	if _, ok := m.issueActivityReceipt("demo", attempt, time.Now()); !ok {
		t.Fatal("one disconnect invalidated an overlapping live connection")
	}
	m.clientDisconnected("demo", attempt)
	if _, ok := m.issueActivityReceipt("demo", attempt, time.Now()); ok {
		t.Fatal("final disconnect left attempt active")
	}
}

func TestReservedActivityReceiptCanBeRestoredForRetry(t *testing.T) {
	m := newTtydManager()
	if _, err := m.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopSession("demo") })
	attempt := "attempt-restore-1234"
	m.clientConnected("demo", attempt)
	now := time.Now()
	receipt, ok := m.issueActivityReceipt("demo", attempt, now)
	if !ok {
		t.Fatal("failed to issue receipt")
	}
	issued, ok := m.reserveActivityReceipt("demo", receipt, now)
	if !ok {
		t.Fatal("failed to reserve receipt")
	}
	m.restoreActivityReceipt(receipt, issued, now)
	if _, ok := m.reserveActivityReceipt("demo", receipt, now); !ok {
		t.Fatal("restored receipt was not retryable")
	}
}

func TestWrongSessionCannotConsumeAnotherSessionsReceipt(t *testing.T) {
	m := newTtydManager()
	if _, err := m.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopSession("demo") })
	attempt := "attempt-owner-12345"
	m.clientConnected("demo", attempt)
	now := time.Now()
	receipt, ok := m.issueActivityReceipt("demo", attempt, now)
	if !ok {
		t.Fatal("failed to issue receipt")
	}
	if _, ok := m.reserveActivityReceipt("other", receipt, now); ok {
		t.Fatal("wrong session consumed receipt")
	}
	if _, ok := m.reserveActivityReceipt("demo", receipt, now); !ok {
		t.Fatal("legitimate session lost receipt after wrong-session attempt")
	}
}

func TestExpiredTerminalActivityReceiptsAreRejectedAndSwept(t *testing.T) {
	m := newTtydManager()
	if _, err := m.startForSession("demo", "sleep", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.stopSession("demo") })
	attempt := "attempt-expiry-12345"
	m.clientConnected("demo", attempt)
	now := time.Now()
	receipt, ok := m.issueActivityReceipt("demo", attempt, now)
	if !ok {
		t.Fatal("failed to issue receipt")
	}
	if m.consumeActivityReceipt("demo", receipt, now.Add(terminalActivityReceiptTTL+time.Second)) {
		t.Fatal("expired receipt was accepted")
	}
	if len(m.activityReceipts) != 0 {
		t.Fatalf("expired receipts were not swept: %d", len(m.activityReceipts))
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
