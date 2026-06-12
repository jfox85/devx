package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
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
	// Desktop WebViews load the private loopback origin directly so ttyd
	// WebSockets are not forced through Wails' asset-server proxy. Bootstrap the
	// non-secret SPA auth marker and an HTTP-only token cookie on HTML shell
	// responses; API/terminal requests then authenticate as ordinary same-origin
	// cookie requests.
	handler := p.bootstrapDesktopAuth(authMiddleware(p.token, mux))
	p.server = &http.Server{Handler: handler}
	return p.server.Serve(p.listener)
}

func (p *PrivateServer) bootstrapDesktopAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsHTML(r) {
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

		rw := newBufferingResponseWriter(w)
		next.ServeHTTP(rw, r)
		body := bytes.Replace(rw.body.Bytes(),
			[]byte("<head>"),
			[]byte("<head><script>localStorage.setItem('devx_authed','1')</script>"),
			1)
		if rw.header.Get("Content-Length") != "" {
			rw.header.Set("Content-Length", strconv.Itoa(len(body)))
		}
		copyHeaders(w.Header(), rw.header)
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

type bufferingResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferingResponseWriter(w http.ResponseWriter) *bufferingResponseWriter {
	return &bufferingResponseWriter{header: w.Header().Clone()}
}

func (b *bufferingResponseWriter) Header() http.Header    { return b.header }
func (b *bufferingResponseWriter) WriteHeader(status int) { b.status = status }
func (b *bufferingResponseWriter) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

func copyHeaders(dst, src http.Header) {
	for k := range dst {
		delete(dst, k)
	}
	for k, values := range src {
		for _, value := range values {
			dst.Add(k, value)
		}
	}
}

func acceptsHTML(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html")
}

func generateEphemeralToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate ephemeral token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
