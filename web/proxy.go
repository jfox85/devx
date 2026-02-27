package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const wsWriteDeadline = 10 * time.Second

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// proxyWebSocket proxies a WebSocket connection to a backend ttyd instance.
func proxyWebSocket(w http.ResponseWriter, r *http.Request, backendPort int) {
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	backendURL := fmt.Sprintf("ws://localhost:%d/ws", backendPort)
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
			backendConn.SetWriteDeadline(time.Now().Add(wsWriteDeadline)) //nolint:errcheck
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
			clientConn.SetWriteDeadline(time.Now().Add(wsWriteDeadline)) //nolint:errcheck
			if err := clientConn.WriteMessage(mt, msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
}
