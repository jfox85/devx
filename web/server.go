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

	"github.com/spf13/viper"
)

// Server is the devx web HTTP server
type Server struct {
	token    string
	port     int
	bind     string
	server   *http.Server
	ttyd     *ttydManager
	terminal *terminalService
	hub      *sseHub
}

// New creates a new Server. token must be non-empty.
func New(token string, port int) (*Server, error) {
	return NewWithBind(token, port, "")
}

// NewWithBind creates a new Server bound to the given address. bind defaults to
// loopback (127.0.0.1) when empty. Binding to 0.0.0.0 is only intended for
// running devx web inside a container where port publishing requires it; it
// must not be used to expose plain HTTP on a real network.
func NewWithBind(token string, port int, bind string) (*Server, error) {
	if token == "" {
		return nil, fmt.Errorf("web_secret_token must be set in config to use devx web")
	}
	if bind == "" {
		bind = "127.0.0.1"
	}
	// Always (re)configure: an empty config must reset to the loopback-only
	// default rather than inherit trust widened by a previous server instance
	// (trustedProxyNets is package state shared with the websocket upgrader).
	if err := configureTrustedProxies(strings.Split(viper.GetString("web_trusted_proxies"), ",")); err != nil {
		return nil, err
	}
	ttyd := newTtydManager()
	return &Server{token: token, port: port, bind: bind, ttyd: ttyd, terminal: newTerminalService(ttyd), hub: newSSEHub()}, nil
}

// Start begins listening and serving.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	// Bind to loopback by default — devx web is a local developer tool and must
	// not be reachable from the network over plain HTTP. NewWithBind allows
	// 0.0.0.0 for in-container use where Docker port publishing requires it.
	s.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.bind, s.port),
		Handler: authMiddleware(s.token, mux),
	}

	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	fmt.Printf("devx web listening on http://%s:%d\n", s.bind, s.port)
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
		needsAuth := strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/terminal/") || strings.HasPrefix(r.URL.Path, "/uploads/") || strings.HasPrefix(r.URL.Path, "/sessions/")
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

		// Check session cookie (constant-time). Cookie-authenticated unsafe methods
		// must include a same-origin Origin header; this prevents a page on another
		// localhost origin from using ambient cookies to mutate DevX state.
		if cookie, err := r.Cookie("devx_token"); err == nil {
			if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1 {
				if isUnsafeMethod(r.Method) && !originMatchesHost(r) {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden origin"})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/terminal/") && !isWebSocketUpgrade(r) && !requestProvenanceMatchesHost(r) {
					writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden origin"})
					return
				}
				next.ServeHTTP(w, r)
				return
			}
		}

		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	})
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// trustedProxyNets holds the peer networks whose forwarded headers
// (X-Forwarded-Host, X-Forwarded-Proto) are honored. Default: loopback only —
// devx's proxies (Caddy, cloudflared) normally run on the same machine.
// Topologies where the proxy peer is not loopback (e.g. the containerized
// dogfooding setup, where Docker port publishing makes the peer the bridge
// gateway) must opt in explicitly via the web_trusted_proxies config
// (comma-separated CIDRs). Connections from peers outside these networks
// never get forwarded-header trust: they fall back to r.Host only.
var trustedProxyNets = defaultTrustedProxyNets()

func defaultTrustedProxyNets() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{"127.0.0.0/8", "::1/128"} {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}

// configureTrustedProxies extends the default loopback trust with the given
// CIDRs. Invalid entries are rejected so a typo fails loudly at startup
// rather than silently widening or narrowing trust.
func configureTrustedProxies(cidrs []string) error {
	nets := defaultTrustedProxyNets()
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("invalid web_trusted_proxies entry %q: %w", c, err)
		}
		nets = append(nets, n)
	}
	trustedProxyNets = nets
	return nil
}

// trustedProxyRequest reports whether the request's direct peer is a trusted
// reverse proxy, i.e. whether forwarded headers may be honored.
func trustedProxyRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// requestIsHTTPS reports whether the client-facing connection used HTTPS:
// either the backend terminated TLS itself, or a trusted proxy forwarded the
// original scheme in X-Forwarded-Proto.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !trustedProxyRequest(r) {
		return false
	}
	xfp := r.Header.Get("X-Forwarded-Proto")
	if first, _, found := strings.Cut(xfp, ","); found {
		xfp = first
	}
	return strings.TrimSpace(xfp) == "https"
}

// effectiveHosts returns the host values that count as "same origin" for this
// request. It always includes r.Host, and additionally includes
// X-Forwarded-Host when the request arrives from a trusted proxy. devx sits
// behind a trusted reverse proxy (Caddy, and Cloudflare's tunnel) that
// rewrites the upstream Host header to "localhost" so dev servers accept the
// request, while forwarding the original external hostname in
// X-Forwarded-Host. Without honoring it, the browser's Origin/Referer (the
// real external host) never matches r.Host ("localhost"), which broke
// same-origin checks for terminal/WebSocket requests reached via Caddy or the
// Cloudflare tunnel.
func effectiveHosts(r *http.Request) []string {
	hosts := []string{r.Host}
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" && trustedProxyRequest(r) {
		// X-Forwarded-Host may be a comma-separated list; the first value is the
		// original client-facing host.
		if first, _, found := strings.Cut(xfh, ","); found {
			xfh = first
		}
		hosts = append(hosts, strings.TrimSpace(xfh))
	}
	return hosts
}

func originMatchesHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, h := range effectiveHosts(r) {
		if origin == "http://"+h || origin == "https://"+h {
			return true
		}
	}
	return false
}

func requestProvenanceMatchesHost(r *http.Request) bool {
	if originMatchesHost(r) {
		return true
	}
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			for _, h := range effectiveHosts(r) {
				if u.Host == h {
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// API routes registered in api.go
	registerAPIRoutes(mux)
	registerArtifactRoutes(mux)
	registerShareRoutes(mux)
	// Flag routes require access to the SSE hub — registered as Server methods.
	// Session name passed as query param (?name=...) so names containing
	// slashes (branch-style names) are handled correctly.
	mux.HandleFunc("POST /api/sessions/flag", s.handleFlagSession)
	mux.HandleFunc("DELETE /api/sessions/flag", s.handleUnflagSession)
	// SSE-only broadcast for CLI notify path (metadata already written by CLI).
	mux.HandleFunc("POST /api/sessions/flag-notify", s.handleFlagNotify)
	// SSE event stream — auth covered by /api/ prefix in authMiddleware.
	mux.HandleFunc("GET /api/events", s.hub.handleEvents)
	mux.HandleFunc("POST /api/artifacts/notify", s.handleArtifactNotify)
	mux.HandleFunc("POST /api/terminal/prewarm", s.handleTerminalPrewarm)
	mux.HandleFunc("GET /api/terminal/status", s.handleTerminalStatus)
	mux.HandleFunc("POST /api/terminal/send-input", s.handleTerminalSendInput)
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
	sessionName, port, err := s.terminal.ProxyTarget(r)
	if err != nil {
		writeTerminalError(w, err)
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

type terminalPrewarmRequest struct {
	Session string `json:"session"`
}

func (s *Server) handleTerminalPrewarm(w http.ResponseWriter, r *http.Request) {
	if !terminalWriteGuard(w, r, 8<<10) {
		return
	}
	var req terminalPrewarmRequest
	if !handleDecodeJSON(w, r, &req) {
		return
	}
	status, err := s.terminal.EnsureReady(req.Session, terminalStartPrewarm)
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleTerminalStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.terminal.Status(r.URL.Query().Get("session"))
	if err != nil {
		writeTerminalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleTerminalSendInput(w http.ResponseWriter, r *http.Request) {
	if !terminalWriteGuard(w, r, terminalSendInputMaxBytes+1024) {
		return
	}
	var req struct {
		Session string `json:"session"`
		Text    string `json:"text"`
		Submit  bool   `json:"submit"`
		Mode    string `json:"mode"`
	}
	if !handleDecodeJSON(w, r, &req) {
		return
	}
	if err := s.terminal.SendInput(req.Session, terminalInput{Text: req.Text, Submit: req.Submit, Mode: req.Mode}); err != nil {
		writeTerminalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
