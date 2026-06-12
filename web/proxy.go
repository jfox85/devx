package web

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const wsWriteDeadline = 60 * time.Second

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
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = injectTerminalCopyOnSelect
	proxy.ServeHTTP(w, r)
}

func injectTerminalCopyOnSelect(resp *http.Response) error {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	body = bytes.Replace(body, []byte("</body>"), []byte(terminalCopyOnSelectScript+"</body>"), 1)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("Content-Encoding")
	return nil
}

const terminalCopyOnSelectScript = `<script>
(function () {
  if (window.__devxCopyOnSelect) return;
  window.__devxCopyOnSelect = true;
  function fallbackCopy(text) {
    try {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      document.body.removeChild(ta);
    } catch (e) {}
  }
  function copySelection() {
    var selection = window.getSelection && String(window.getSelection());
    if (!selection || !selection.trim()) return;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(selection).catch(function () { fallbackCopy(selection); });
    } else {
      fallbackCopy(selection);
    }
  }
  document.addEventListener('mouseup', function () { setTimeout(copySelection, 0); }, true);
  document.addEventListener('touchend', function () { setTimeout(copySelection, 0); }, true);
})();
</script>`
