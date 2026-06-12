package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// PrivateServer is the desktop-shell topology from the control deck plan
// (0E): a per-launch DevX web server bound to a random loopback port with an
// ephemeral token that lives only in process memory. The desktop host reads
// Addr/Token via native bindings and injects them into the privileged
// WebView; the token is never placed in URLs, CLI args, logs, or persisted
// storage.
type PrivateServer struct {
	*Server
	listener               net.Listener
	token                  string
	terminalBootstrapToken string
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
	terminalBootstrapToken, err := generateEphemeralToken()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return &PrivateServer{Server: srv, listener: ln, token: token, terminalBootstrapToken: terminalBootstrapToken}, nil
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

// TerminalBootstrapToken returns a per-launch token scoped to bootstrapping
// direct terminal iframes from the Wails shell. It is not the main API token:
// presenting it to /terminal/<session> sets the HTTP-only devx_token cookie
// and redirects to the clean terminal URL so ttyd WebSockets can authenticate
// directly against the private loopback origin.
func (p *PrivateServer) TerminalBootstrapToken() string {
	return p.terminalBootstrapToken
}

// Serve blocks serving HTTP on the pre-bound listener.
func (p *PrivateServer) Serve() error {
	mux := http.NewServeMux()
	p.registerRoutes(mux)
	// Keep normal auth on API/static routes, but allow a direct terminal iframe
	// to bootstrap an HTTP-only auth cookie. This avoids Wails' websocket-hostile
	// asset-server proxy for ttyd while keeping the SPA itself on wails://.
	handler := p.bootstrapTerminalAuth(authMiddleware(p.token, mux))
	p.server = &http.Server{Handler: handler}
	return p.server.Serve(p.listener)
}

func (p *PrivateServer) bootstrapTerminalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/terminal/") {
			next.ServeHTTP(w, r)
			return
		}
		provided := r.URL.Query().Get("desktop_token")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(p.terminalBootstrapToken)) != 1 {
			next.ServeHTTP(w, r)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "devx_token",
			Value:    p.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		clean := *r.URL
		q := clean.Query()
		q.Del("desktop_token")
		clean.RawQuery = q.Encode()
		http.Redirect(w, r, clean.String(), http.StatusTemporaryRedirect)
	})
}

func generateEphemeralToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate ephemeral token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
