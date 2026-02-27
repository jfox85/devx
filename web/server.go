package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Server is the devx web HTTP server
type Server struct {
	token  string
	port   int
	server *http.Server
	ttyd   *ttydManager
}

// New creates a new Server. token must be non-empty.
func New(token string, port int) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("web_secret_token must be set in config to use devx web")
	}
	return &Server{token: token, port: port, ttyd: newTtydManager()}, nil
}

// Start begins listening and serving.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
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
		needsAuth := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/terminal/")
		if !needsAuth || r.URL.Path == "/api/login" {
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization: Bearer <token>
		if r.Header.Get("Authorization") == "Bearer "+token {
			next.ServeHTTP(w, r)
			return
		}

		// Check session cookie
		if cookie, err := r.Cookie("devx_token"); err == nil && cookie.Value == token {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// API routes registered in api.go
	registerAPIRoutes(mux)
	// Static SPA served from embedded FS (registered in embed.go)
	registerStaticRoutes(mux)
	// Catch-all for /terminal/* — handles both iframe HTTP requests and WebSocket upgrades.
	// Auth is enforced by authMiddleware (covers the /terminal/ prefix).
	mux.HandleFunc("/terminal/", s.handleTerminalProxy)
}

// handleTerminalProxy handles all /terminal/{session}/* traffic.
// WebSocket upgrade requests are proxied via gorilla/websocket.
// Plain HTTP requests (iframe asset loads) are reverse-proxied via httputil.
func (s *Server) handleTerminalProxy(w http.ResponseWriter, r *http.Request) {
	// Parse session name from path: /terminal/{session}/...
	path := strings.TrimPrefix(r.URL.Path, "/terminal/")
	sessionName, _, _ := strings.Cut(path, "/")
	if sessionName == "" {
		http.Error(w, "missing session name", http.StatusBadRequest)
		return
	}

	port, err := s.ttyd.startForSession(sessionName)
	if err != nil {
		http.Error(w, "failed to start terminal: "+err.Error(), http.StatusInternalServerError)
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
