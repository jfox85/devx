package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPrivateServerTopology(t *testing.T) {
	p, err := NewPrivateServer()
	if err != nil {
		t.Fatalf("NewPrivateServer: %v", err)
	}

	if !strings.HasPrefix(p.Addr(), "127.0.0.1:") {
		t.Fatalf("private server must bind loopback, got %q", p.Addr())
	}
	if len(p.Token()) != 64 {
		t.Fatalf("expected 64-char hex token, got %d chars", len(p.Token()))
	}

	// Two instances must never share a token (per-launch ephemeral).
	p2, err := NewPrivateServer()
	if err != nil {
		t.Fatalf("second NewPrivateServer: %v", err)
	}
	if p.Token() == p2.Token() {
		t.Fatal("ephemeral tokens must be unique per launch")
	}
	p2.listener.Close()

	go p.Serve() //nolint:errcheck
	defer p.Shutdown(context.Background())

	base := "http://" + p.Addr()
	client := &http.Client{Timeout: 2 * time.Second}

	// Unauthenticated API access is rejected.
	resp, err := client.Get(base + "/api/sessions")
	if err != nil {
		t.Fatalf("GET /api/sessions: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Bearer token (as the desktop host would inject) is accepted.
	req, _ := http.NewRequest("GET", base+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+p.Token())
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d: %s", resp.StatusCode, body)
	}

	// The SPA shell is served (desktop WebView loads this).
	resp, err = client.Get(base + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	shell, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(shell), "<html") {
		t.Fatalf("expected SPA shell, got %d (%d bytes)", resp.StatusCode, len(shell))
	}

	// A wrong token is rejected (constant-time compare path).
	req, _ = http.NewRequest("GET", base+"/api/sessions", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", strings.Repeat("0", 64)))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET with wrong token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}
}
