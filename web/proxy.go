package web

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const wsWriteDeadline = 60 * time.Second

var upgrader = websocket.Upgrader{
	// Only allow requests whose Origin matches the server's own host.
	// This prevents cross-site WebSocket hijacking: a malicious page opened
	// in the same browser cannot connect to ws://localhost:<port>/terminal/*
	// because its Origin won't match r.Host.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			// No Origin header = same-origin browser request; allow it.
			return true
		}
		return origin == "http://"+r.Host || origin == "https://"+r.Host
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
