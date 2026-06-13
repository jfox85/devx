package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/jfox85/devx/target"
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
	srv, err := NewWithBind(token, 0, "127.0.0.1", target.GatepostRuntimeConfig{})
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

// TerminalBootstrapToken returns a per-launch token scoped to direct terminal
// iframes from the Wails shell. It is not the main API token: presenting it as
// desktop_token on /terminal/<session> authorizes only terminal traffic. The
// token stays in the iframe URL rather than using a cookie redirect because
// the iframe is cross-site relative to wails:// and WebKit blocks SameSite
// cookies in that third-party context.
func (p *PrivateServer) TerminalBootstrapToken() string {
	return p.terminalBootstrapToken
}

// Serve blocks serving HTTP on the pre-bound listener.
func (p *PrivateServer) Serve() error {
	mux := http.NewServeMux()
	p.registerRoutes(mux)
	// Keep normal auth on API/static routes, but allow direct terminal iframes
	// to authenticate with the terminal-scoped desktop_token. This avoids Wails'
	// websocket-hostile asset-server proxy for ttyd while keeping the SPA itself
	// on wails://.
	handler := p.authenticateTerminalToken(authMiddleware(p.token, mux))
	p.server = &http.Server{Handler: handler}
	return p.server.Serve(p.listener)
}

func (p *PrivateServer) authenticateTerminalToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/terminal/") {
			next.ServeHTTP(w, r)
			return
		}
		provided := r.URL.Query().Get("desktop_token")
		if provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(p.terminalBootstrapToken)) == 1 {
			// Reuse the existing auth middleware path. This applies to both the
			// initial ttyd HTML request and any websocket request that preserves
			// location.search (ttyd does on current builds).
			r.Header.Set("Authorization", "Bearer "+p.token)
		}
		next.ServeHTTP(w, r)
	})
}

func generateEphemeralToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate ephemeral token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
