package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
)

// PrivateServer is the desktop-shell topology from the control deck plan
// (0E): a per-launch DevX web server bound to a random loopback port with an
// ephemeral token that lives only in process memory. The desktop host reads
// Addr/Token via native bindings and injects them into the privileged
// WebView; the token is never placed in URLs, CLI args, logs, or persisted
// storage.
type PrivateServer struct {
	*Server
	listener net.Listener
	token    string
}

// NewPrivateServer creates a private server on a random loopback port with a
// freshly generated ephemeral token.
func NewPrivateServer() (*PrivateServer, error) {
	token, err := generateEphemeralToken()
	if err != nil {
		return nil, err
	}
	srv, err := NewWithBind(token, 0, "127.0.0.1")
	if err != nil {
		return nil, err
	}
	// Bind immediately so Addr() is valid before Serve starts; port 0 lets the
	// kernel pick a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind private server: %w", err)
	}
	return &PrivateServer{Server: srv, listener: ln, token: token}, nil
}

// Addr returns the bound loopback address, e.g. "127.0.0.1:49213".
func (p *PrivateServer) Addr() string {
	return p.listener.Addr().String()
}

// Token returns the ephemeral per-launch token. Callers must keep it in
// memory only.
func (p *PrivateServer) Token() string {
	return p.token
}

// Serve blocks serving HTTP on the pre-bound listener.
func (p *PrivateServer) Serve() error {
	mux := http.NewServeMux()
	p.registerRoutes(mux)
	p.server = &http.Server{Handler: authMiddleware(p.token, mux)}
	return p.server.Serve(p.listener)
}

func generateEphemeralToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate ephemeral token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
