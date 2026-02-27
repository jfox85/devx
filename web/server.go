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

// authMiddleware enforces token auth on all /api/* routes.
// Non-API routes (static assets, login) pass through unauthenticated.
func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/login" {
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
	// WebSocket proxy for ttyd terminal access
	mux.HandleFunc("/terminal/{session}/ws", s.handleTerminalWS)
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	sessionName := r.PathValue("session")
	port, err := s.ttyd.startForSession(sessionName)
	if err != nil {
		http.Error(w, "failed to start terminal: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.ttyd.clientConnected(sessionName)
	defer s.ttyd.clientDisconnected(sessionName)
	proxyWebSocket(w, r, port)
}
