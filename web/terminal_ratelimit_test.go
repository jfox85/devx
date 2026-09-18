package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sendInputRequest builds a same-origin terminal write request like the desktop
// keyboard proxy issues for each debounced batch of typed text.
func sendInputRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/terminal/send-input",
		strings.NewReader(`{"session":"demo","text":"a"}`))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "http://127.0.0.1:7777")
	return req
}

// The desktop shell relays every keystroke over HTTP because its terminal
// iframe is cross-origin, so ordinary typing must not be throttled as abuse.
// Before the exemption, typing died after 30 batches with a silent 429.
func TestPrivateServerExemptsTerminalWritesFromRateLimit(t *testing.T) {
	terminalWrites = newSimpleRateLimiter(terminalWriteRateLimit, terminalWriteRateWindow)
	srv := &Server{terminalWritesExempt: true}

	for i := 1; i <= terminalWriteRateLimit*3; i++ {
		rec := httptest.NewRecorder()
		if !srv.terminalGuard(rec, sendInputRequest(), terminalSendInputMaxBytes+1024) {
			t.Fatalf("exempt server rejected terminal write #%d with status %d: %s",
				i, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// A non-exempt server (the ordinary `devx web` daemon, which may be reachable
// through Caddy or a tunnel) keeps its abuse protection.
func TestNonExemptServerStillRateLimitsTerminalWrites(t *testing.T) {
	terminalWrites = newSimpleRateLimiter(terminalWriteRateLimit, terminalWriteRateWindow)
	srv := &Server{}

	for i := 1; i <= terminalWriteRateLimit; i++ {
		rec := httptest.NewRecorder()
		if !srv.terminalGuard(rec, sendInputRequest(), terminalSendInputMaxBytes+1024) {
			t.Fatalf("write #%d rejected before the limit: %d", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	if srv.terminalGuard(rec, sendInputRequest(), terminalSendInputMaxBytes+1024) {
		t.Fatal("expected the limiter to reject the write past the budget")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

// The exemption must only relax the rate limit. Cross-origin terminal writes
// stay forbidden even on the private desktop server.
func TestExemptServerStillEnforcesOriginCheck(t *testing.T) {
	terminalWrites = newSimpleRateLimiter(terminalWriteRateLimit, terminalWriteRateWindow)
	srv := &Server{terminalWritesExempt: true}

	req := sendInputRequest()
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()

	if srv.terminalGuard(rec, req, terminalSendInputMaxBytes+1024) {
		t.Fatal("cross-origin terminal write should be rejected even when exempt")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// The rate limit still has to cover a human typing on the non-exempt path: at
// one request per debounced batch, 30/minute was less than a sentence.
func TestTerminalWriteRateLimitCoversHumanTyping(t *testing.T) {
	if terminalWriteRateLimit < 300 {
		t.Fatalf("terminalWriteRateLimit = %d is too small for sustained typing", terminalWriteRateLimit)
	}
}

// The desktop private server must actually set the exemption.
func TestNewPrivateServerSetsTerminalWriteExemption(t *testing.T) {
	priv, err := NewPrivateServer()
	if err != nil {
		t.Fatalf("new private server: %v", err)
	}
	t.Cleanup(func() { _ = priv.listener.Close() })

	if !priv.terminalWritesExempt {
		t.Fatal("private desktop server should exempt terminal writes from the rate limiter")
	}
}
