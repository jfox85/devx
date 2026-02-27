package web

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const wsWriteDeadline = 10 * time.Second

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// proxyWebSocket proxies a WebSocket connection to a backend ttyd instance at wsPath.
func proxyWebSocket(w http.ResponseWriter, r *http.Request, backendPort int, wsPath string) {
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	backendURL := fmt.Sprintf("ws://localhost:%d%s", backendPort, wsPath)
	backendConn, _, err := websocket.DefaultDialer.Dial(backendURL, nil)
	if err != nil {
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
