package web

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const wsWriteDeadline = 60 * time.Second

// effectiveHosts returns the host values that count as "same origin" for this
// request. It always includes r.Host, and additionally includes
// X-Forwarded-Host when present. devx sits behind a trusted reverse proxy
// (Caddy, and Cloudflare's tunnel) that rewrites the upstream Host header to
// "localhost" so dev servers accept the request, while forwarding the original
// external hostname in X-Forwarded-Host. Without honoring it, the browser's
// Origin (the real external host) never matches r.Host ("localhost"), which
// rejected terminal WebSocket upgrades reached via Caddy or the Cloudflare
// tunnel.
func effectiveHosts(r *http.Request) []string {
	hosts := []string{r.Host}
	if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
		// X-Forwarded-Host may be a comma-separated list; the first value is the
		// original client-facing host.
		if first, _, found := strings.Cut(xfh, ","); found {
			xfh = first
		}
		hosts = append(hosts, strings.TrimSpace(xfh))
	}
	return hosts
}

var upgrader = websocket.Upgrader{
	// Only allow requests whose Origin matches the server's own host.
	// This prevents cross-site WebSocket hijacking: a malicious page opened
	// in the same browser cannot connect to ws://localhost:<port>/terminal/*
	// because its Origin won't match the request host.
	//
	// effectiveHosts honors X-Forwarded-Host so the check still passes when the
	// request is reached via the trusted Caddy reverse proxy / Cloudflare tunnel,
	// which rewrite the upstream Host header to "localhost".
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// No Origin header = same-origin browser request; allow it.
			return true
		}
		for _, h := range effectiveHosts(r) {
			if origin == "http://"+h || origin == "https://"+h {
				return true
			}
		}
		return false
	},
	Subprotocols: []string{"tty"},
}

// proxyWebSocket proxies a WebSocket connection to a backend ttyd instance at wsPath.
func proxyWebSocket(w http.ResponseWriter, r *http.Request, backendPort int, wsPath string) {
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	// Forward the Sec-WebSocket-Protocol header so ttyd accepts the connection.
	backendHeader := http.Header{}
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		backendHeader.Set("Sec-WebSocket-Protocol", proto)
	}
	backendURL := fmt.Sprintf("ws://localhost:%d%s", backendPort, wsPath)

	// Retry the backend dial: portForSession may return a port before waitForPort
	// completes (the port is in the map as soon as the process starts). Retry for
	// up to 5 s to handle this startup race.
	var backendConn *websocket.Conn
	for range 20 {
		var dialErr error
		backendConn, _, dialErr = websocket.DefaultDialer.Dial(backendURL, backendHeader)
		if dialErr == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if backendConn == nil {
		// Best-effort close message; if it fails, defer will still close the connection.
		_ = clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "backend unavailable"))
		return
	}
	defer backendConn.Close()

	errc := make(chan error, 2)

	go func() {
		for {
			mt, msg, err := clientConn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			// SetWriteDeadline errors are non-fatal; a subsequent write will fail and propagate.
			_ = backendConn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
			if err := backendConn.WriteMessage(mt, msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	go func() {
		for {
			mt, msg, err := backendConn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			// SetWriteDeadline errors are non-fatal; a subsequent write will fail and propagate.
			_ = clientConn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
			if err := clientConn.WriteMessage(mt, msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
}

// proxyHTTP reverse-proxies an HTTP request to a backend ttyd instance.
func proxyHTTP(w http.ResponseWriter, r *http.Request, backendPort int) {
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("localhost:%d", backendPort),
	}
	httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
}
