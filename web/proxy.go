package web

import (
	"bytes"
	"compress/gzip"
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
	// Dial 127.0.0.1 explicitly: ttyd binds IPv4 loopback only (-i 127.0.0.1), but
	// "localhost" can resolve to ::1 first (it maps to both in /etc/hosts), which
	// ttyd refuses, intermittently breaking the terminal proxy.
	backendURL := fmt.Sprintf("ws://127.0.0.1:%d%s", backendPort, wsPath)

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
		// 127.0.0.1, not "localhost": ttyd binds IPv4 loopback only, and localhost
		// may resolve to ::1 first, which ttyd refuses.
		Host:   fmt.Sprintf("127.0.0.1:%d", backendPort),
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		// We rewrite ttyd HTML to inject desktop helpers; ask the backend for
		// identity encoding so the browser never receives rewritten compressed
		// bytes as plain text.
		req.Header.Del("Accept-Encoding")
	}
	proxy.ModifyResponse = injectTerminalCopyOnSelect
	proxy.ServeHTTP(w, r)
}

func injectTerminalCopyOnSelect(resp *http.Response) error {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	encoding := strings.ToLower(resp.Header.Get("Content-Encoding"))
	var reader io.Reader = resp.Body
	if encoding == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return err
		}
		defer gz.Close()
		reader = gz
	} else if encoding != "" && encoding != "identity" {
		// Unknown compression (e.g. br): leave response untouched rather than
		// corrupting it by removing Content-Encoding after a partial rewrite.
		return nil
	}
	body, err := io.ReadAll(reader)
	resp.Body.Close()
	if err != nil {
		return err
	}
	body = bytes.Replace(body, []byte("</head>"), []byte(terminalHeadAddons+"</head>"), 1)
	body = bytes.Replace(body, []byte("</body>"), []byte(terminalCopyOnSelectScript+terminalPasteBridgeScript+"</body>"), 1)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	resp.Header.Del("Content-Encoding")
	return nil
}

const terminalHeadAddons = `<link rel="stylesheet" href="/nerd-font.css">
<style>
html, body {
  height: 100%;
  margin: 0;
  overflow: hidden;
  overscroll-behavior: none;
}
.xterm, .xterm-viewport {
  touch-action: pan-y !important;
}
.xterm-viewport {
  -webkit-overflow-scrolling: touch !important;
  overscroll-behavior-y: contain;
}
</style>`

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

// terminalPasteBridgeScript forwards image pastes from inside the (cross-origin)
// ttyd terminal iframe up to the parent SPA via postMessage. The SPA cannot add
// a paste listener to the terminal iframe directly because, in the desktop app,
// the iframe is served from the private loopback origin and contentDocument is
// cross-origin. xterm would otherwise paste raw image bytes into the shell as
// garbage. When a clipboard image File is present it is shipped as a data URL;
// when it is not (WKWebView omits the File, or the clipboard holds a file URL),
// a flag tells the parent to fall back to the native clipboard read.
const terminalPasteBridgeScript = `<script>
(function () {
  if (window.__devxPasteBridge) return;
  window.__devxPasteBridge = true;
  document.addEventListener('paste', function (e) {
    var items = (e.clipboardData && e.clipboardData.items) || [];
    for (var i = 0; i < items.length; i++) {
      var it = items[i];
      if (it.kind === 'file' && it.type && it.type.indexOf('image/') === 0) {
        var file = it.getAsFile();
        if (!file) continue;
        e.preventDefault();
        e.stopPropagation();
        var reader = new FileReader();
        reader.onload = function () {
          try {
            parent.postMessage({
              type: 'devx:terminal-image-paste',
              name: file.name || 'clipboard.png',
              mime: file.type || 'image/png',
              dataURL: reader.result,
            }, '*');
          } catch (err) {}
        };
        reader.readAsDataURL(file);
        return;
      }
    }
    // No image File on the clipboard. In the desktop app this is also the path
    // for clipboard images WKWebView hides and for file-URL clipboards (e.g.
    // Raycast "paste recent screenshot"); ask the parent to try the native
    // clipboard. Only do so when there is no text, so normal text paste into
    // the shell is never intercepted.
    var text = (e.clipboardData && e.clipboardData.getData('text/plain')) || '';
    if (!text) {
      try { parent.postMessage({ type: 'devx:terminal-clipboard-image' }, '*'); } catch (err) {}
    }
  }, true);
})();
</script>`
