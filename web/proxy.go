package web

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

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
		clientConn.WriteMessage(websocket.CloseMessage, //nolint:errcheck
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
			if err := clientConn.WriteMessage(mt, msg); err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
}
