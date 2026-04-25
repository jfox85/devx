package web

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jfox85/devx/session"
)

// Server is the devx web HTTP server
type Server struct {
	token  string
	port   int
	server *http.Server
	ttyd   *ttydManager
	hub    *sseHub
}

// New creates a new Server. token must be non-empty.
func New(token string, port int) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("web_secret_token must be set in config to use devx web")
	}
	return &Server{token: token, port: port, ttyd: newTtydManager(), hub: newSSEHub()}, nil
}

// Start begins listening and serving.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	// Bind to loopback only — devx web is a local developer tool and must not
	// be reachable from the network over plain HTTP.
	s.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler: authMiddleware(s.token, mux),
	}

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	fmt.Printf("devx web listening on http://localhost:%d\n", s.port)
	return s.server.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// authMiddleware enforces token auth on all /api/* and /terminal/* routes.
// Non-API/terminal routes (static assets, login) pass through unauthenticated.
func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		needsAuth := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/terminal/") || strings.HasPrefix(r.URL.Path, "/uploads/")
		if !needsAuth || r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization: Bearer <token> (constant-time to prevent timing attacks)
		bearer := []byte("Bearer " + token)
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), bearer) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		// Check session cookie (constant-time)
		if cookie, err := r.Cookie("devx_token"); err == nil {
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	})
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// API routes registered in api.go
	registerAPIRoutes(mux)
	// Flag routes require access to the SSE hub — registered as Server methods.
	// Session name passed as query param (?name=...) so names containing
	// slashes (branch-style names) are handled correctly.
	mux.HandleFunc("POST /api/sessions/flag", s.handleFlagSession)
	mux.HandleFunc("DELETE /api/sessions/flag", s.handleUnflagSession)
	// SSE-only broadcast for CLI notify path (metadata already written by CLI).
	mux.HandleFunc("POST /api/sessions/flag-notify", s.handleFlagNotify)
	// SSE event stream — auth covered by /api/ prefix in authMiddleware.
	mux.HandleFunc("GET /api/events", s.hub.handleEvents)
	// Remote show — uploads a file and broadcasts to all SSE clients.
	mux.HandleFunc("POST /api/show", s.handleShow)
	// Static SPA served from embedded FS (registered in embed.go)
	registerStaticRoutes(mux)
	// Catch-all for /terminal/* — handles both iframe HTTP requests and WebSocket upgrades.
	// Auth is enforced by authMiddleware (covers the /terminal/ prefix).
	mux.HandleFunc("/terminal/", s.handleTerminalProxy)
}

// handleTerminalProxy handles all /terminal/* traffic.
// WebSocket upgrade requests are proxied via gorilla/websocket.
// Plain HTTP requests (iframe asset loads) are reverse-proxied via httputil.
//
// Session name resolution uses two strategies:
//  1. Parse the %2F-encoded session name from RawPath (initial iframe/WS request
//     from Terminal.svelte, which uses encodeURIComponent on the session name).
//  2. Prefix-match against active ttyd sessions (subsequent asset requests from
//     ttyd's own HTML, which uses decoded slashes in asset hrefs).
func (s *Server) handleTerminalProxy(w http.ResponseWriter, r *http.Request) {
	sessionName, port, err := s.resolveTerminalSession(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sessionName == "" {
		http.Error(w, "missing or unknown session", http.StatusNotFound)
		return
	}

	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		s.ttyd.clientConnected(sessionName)
		defer s.ttyd.clientDisconnected(sessionName)
		proxyWebSocket(w, r, port, r.URL.Path)
		return
	}

	proxyHTTP(w, r, port)
}

// resolveTerminalSession determines the session name and ttyd port for a /terminal/* request.
func (s *Server) resolveTerminalSession(r *http.Request) (sessionName string, port int, err error) {
	// Parse the first path segment, preserving %2F encoding.
	// Terminal.svelte uses encodeURIComponent(session.name) so slashes become %2F.
	rawPath := r.URL.RawPath
	if rawPath == "" {
		rawPath = r.URL.Path
	}
	encodedPart, _, _ := strings.Cut(strings.TrimPrefix(rawPath, "/terminal/"), "/")
	decoded, _ := url.PathUnescape(encodedPart)

	// 1. Exact lookup: session is already running with this name.
	if decoded != "" {
		if p, ok := s.ttyd.portForSession(decoded); ok {
			return decoded, p, nil
		}
	}

	// 2. Prefix-match against active sessions — handles asset requests from ttyd's HTML
	//    where slashes are unencoded (e.g. /terminal/claude/session-name/js/app.js).
	//    Check this BEFORE starting, to avoid a 5-second waitForPort timeout on every asset.
	decodedPath := strings.TrimPrefix(r.URL.Path, "/terminal/")
	if name, p, ok := s.ttyd.findSessionByPathPrefix(decodedPath); ok {
		return name, p, nil
	}

	// 3. Start a new ttyd instance — only reached on the initial request.
	if decoded == "" {
		return "", 0, nil
	}
	// Validate that decoded is a known devx session before starting ttyd.
	// This prevents authenticated users from attaching to arbitrary tmux sessions
	// that were not created by devx (e.g. other users' sessions on a shared host).
	if !session.IsValidSessionName(decoded) {
		return "", 0, nil // treat as missing → 404
	}
	store, err := session.LoadSessions()
	if err != nil {
		return "", 0, fmt.Errorf("could not load session store: %w", err)
	}
	if store == nil || store.Sessions == nil {
		return "", 0, nil // no sessions exist → 404
	}
	sess, ok := store.Sessions[decoded]
	if !ok {
		return "", 0, nil // not a devx-managed session → 404
	}
	// Before starting ttyd, ensure the tmux session is alive. If the machine
	// was rebooted the session will be in metadata but not in tmux; this
	// re-runs tmuxp so the full pane layout and startup commands are restored.
	if err := session.EnsureTmuxSession(decoded, sess.Path); err != nil {
		// Log to a file since the TUI captures stderr.
		logWebError("EnsureTmuxSession(%q, %q): %v", decoded, sess.Path, err)
		return "", 0, fmt.Errorf("failed to restore tmux session %q: %w", decoded, err)
	}
	p, startErr := s.ttyd.startForSession(decoded)
	if startErr != nil {
		return "", 0, fmt.Errorf("failed to start terminal: %s", startErr)
	}
	return decoded, p, nil
}

// logWebError writes a timestamped error to ~/.devx/web.log for debugging
// issues that occur in the web server goroutine (whose stderr the TUI captures).
func logWebError(format string, args ...any) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := home + "/.devx"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(dir+"/web.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, time.Now().Format(time.RFC3339)+" web: "+format+"\n", args...)
}
