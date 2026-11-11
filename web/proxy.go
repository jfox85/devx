package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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

// proxyWebSocket proxies a WebSocket connection to a backend ttyd instance at
// wsPath. onEstablished runs only after both websocket handshakes succeed; its
// returned cleanup runs when that exact proxied connection closes.
func proxyWebSocket(w http.ResponseWriter, r *http.Request, backendPort int, wsPath string, onEstablished func() func()) {
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
	var onClosed func()
	if onEstablished != nil {
		onClosed = onEstablished()
	}
	if onClosed != nil {
		defer onClosed()
	}

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
		Host: fmt.Sprintf("127.0.0.1:%d", backendPort),
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
	attempt := r.URL.Query().Get("devx_attempt")
	proxy.ModifyResponse = func(resp *http.Response) error {
		return injectTerminalResponse(resp, attempt)
	}
	proxy.ServeHTTP(w, r)
}

func injectTerminalCopyOnSelect(resp *http.Response) error {
	return injectTerminalResponse(resp, "")
}

func injectTerminalResponse(resp *http.Response, attempt string) error {
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
	headAddons := terminalHeadAddons
	if validTerminalAttempt(attempt) {
		encodedAttempt, _ := json.Marshal(attempt)
		headAddons += fmt.Sprintf(`<script>
(() => {
  const NativeWebSocket = window.WebSocket;
  function DevxWebSocket(url, protocols) {
    const scoped = new URL(url, window.location.href);
    scoped.searchParams.set('devx_attempt', %s);
    return protocols === undefined ? new NativeWebSocket(scoped) : new NativeWebSocket(scoped, protocols);
  }
  DevxWebSocket.prototype = NativeWebSocket.prototype;
  Object.setPrototypeOf(DevxWebSocket, NativeWebSocket);
  window.WebSocket = DevxWebSocket;
})();
</script>`, encodedAttempt)
	}
	body = bytes.Replace(body, []byte("</head>"), []byte(headAddons+"</head>"), 1)
	body = bytes.Replace(body, []byte("</body>"), []byte(terminalHelperScript+terminalTouchScrollScript+terminalPasteBridgeScript+"</body>"), 1)
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

const terminalHelperScript = `<script>
(function () {
  if (window.__devxTerminalHelpers) return;
  window.__devxTerminalHelpers = true;
  window.__devxCopyOnSelect = true;
  function focusTerminalInput() {
    var attempts = 0;
    function tryFocus() {
      attempts++;
      var textarea = document.querySelector('.xterm-helper-textarea');
      if (textarea) {
        textarea.focus();
        return;
      }
      if (attempts < 12) setTimeout(tryFocus, 50);
    }
    tryFocus();
  }
  window.addEventListener('message', function (event) {
    if (event.source !== window.parent) return;
    if (event && event.data && event.data.type === 'devx:focus-terminal') focusTerminalInput();
  });
  window.addEventListener('focus', focusTerminalInput);
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

  // Desktop (Wails) link handling. The terminal iframe is cross-origin to the
  // wails:// parent, so xterm's window.open-based link opener is swallowed by
  // the WebView. When loaded inside the desktop shell (signalled by the
  // desktop_token query param), forward terminal URLs to the parent via
  // postMessage; the parent routes them to the native OpenExternal binding.
  // In a normal browser this code does nothing and window.open works as usual.
  try {
    if (new URLSearchParams(window.location.search).has('desktop_token')) {
      var openExternal = function (href) {
        if (href && /^https?:\/\//i.test(href)) {
          try { window.parent.postMessage({ type: 'devx:openExternal', url: href }, '*'); } catch (e) {}
          return true;
        }
        return false;
      };
      var nativeOpen = window.open ? window.open.bind(window) : null;
      window.open = function (url) {
        var href = typeof url === 'string' ? url : (url && url.toString ? url.toString() : '');
        if (href && openExternal(href)) return null;
        if (!href) {
          // xterm's default web-links handler calls window.open() with NO url,
          // then assigns returnedWindow.location.href = uri. Hand back a stub
          // whose location.href setter forwards that deferred assignment.
          var loc = { set href(h) { openExternal(h); } };
          // focus/blur/close/resizeTo are no-op stubs so any xterm version that
          // pokes the returned window handle doesn't throw before/after setting
          // location.href.
          return {
            opener: null, closed: false,
            focus: function () {}, blur: function () {}, close: function () {}, resizeTo: function () {},
            get location() { return loc; },
            set location(h) { openExternal(typeof h === 'string' ? h : (h && h.href)); }
          };
        }
        return nativeOpen ? nativeOpen(url) : null;
      };
    }
  } catch (e) {}
})();
</script>`

// terminalTouchScrollScript makes swipe scrolling work while the program in the
// pane has mouse reporting enabled. Fullscreen TUIs such as Pi, Claude Code,
// vim, or tmux with `mouse on` draw on the alternate screen and own their
// scrolling, so xterm.js has no scrollback of its own. In that state xterm.js
// also ignores touch input entirely (it only scrolls its local viewport when
// mouse events are inactive), leaving swipes on a phone dead. This script
// translates vertical swipes, plus a decaying flick, into wheel events on the
// xterm screen; xterm.js encodes them as mouse-wheel reports for the program,
// exactly like a desktop scroll wheel. When mouse reporting is off it does
// nothing, so plain shells keep native xterm scrollback scrolling.
const terminalTouchScrollScript = `<script>
(function () {
  if (window.__devxTouchScroll) return;
  window.__devxTouchScroll = true;
  var LOCK_PX = 8;            // movement before a touch counts as a swipe
  var MAX_LINES_PER_STEP = 12; // cap wheel events per move/frame
  var MIN_FLING = 0.25;       // px/ms needed to start momentum
  var MAX_FLING = 4;          // px/ms
  var tracking = false, locked = false;
  var startX = 0, startY = 0, lastX = 0, lastY = 0, lastTime = 0;
  var velocity = 0, carry = 0, flingFrame = 0;

  function mouseReporting() {
    var t = window.term;
    var mode = t && t.modes && t.modes.mouseTrackingMode;
    return !!mode && mode !== 'none';
  }
  function screenEl() { return document.querySelector('.xterm-screen'); }
  function rowHeight(screen) {
    var t = window.term;
    var h = screen ? screen.getBoundingClientRect().height : 0;
    return t && t.rows && h > 0 ? h / t.rows : 16;
  }
  // Positive px scrolls toward newer output (finger moving up), matching
  // xterm.js's own touch handling and a positive wheel deltaY.
  function scrollPixels(px) {
    var screen = screenEl();
    if (!screen) return;
    var rh = rowHeight(screen);
    carry += px;
    var lines = carry > 0 ? Math.floor(carry / rh) : Math.ceil(carry / rh);
    if (!lines) return;
    carry -= lines * rh;
    var dir = lines > 0 ? 1 : -1;
    var count = Math.min(Math.abs(lines), MAX_LINES_PER_STEP);
    var rect = screen.getBoundingClientRect();
    var x = Math.min(Math.max(lastX, rect.left + 1), rect.right - 1);
    var y = Math.min(Math.max(lastY, rect.top + 1), rect.bottom - 1);
    for (var i = 0; i < count; i++) {
      screen.dispatchEvent(new WheelEvent('wheel', {
        deltaY: dir, deltaMode: WheelEvent.DOM_DELTA_LINE,
        clientX: x, clientY: y, bubbles: true, cancelable: true
      }));
    }
  }
  function stopFling() {
    if (flingFrame) cancelAnimationFrame(flingFrame);
    flingFrame = 0;
  }
  function startFling() {
    var v = Math.max(-MAX_FLING, Math.min(MAX_FLING, velocity));
    if (Math.abs(v) < MIN_FLING) return;
    var prev = performance.now();
    function step(now) {
      var dt = Math.min(now - prev, 50);
      prev = now;
      scrollPixels(v * dt);
      v *= Math.pow(0.95, dt / 16);
      if (Math.abs(v) < 0.05 || !mouseReporting()) { flingFrame = 0; return; }
      flingFrame = requestAnimationFrame(step);
    }
    flingFrame = requestAnimationFrame(step);
  }

  document.addEventListener('touchstart', function (e) {
    stopFling();
    tracking = e.touches.length === 1 && mouseReporting();
    locked = false;
    if (!tracking) return;
    var t = e.touches[0];
    startX = lastX = t.clientX;
    startY = lastY = t.clientY;
    lastTime = e.timeStamp;
    velocity = 0;
    carry = 0;
  }, { capture: true, passive: true });

  document.addEventListener('touchmove', function (e) {
    if (!tracking) return;
    if (e.touches.length !== 1) { tracking = false; return; }
    var t = e.touches[0];
    if (!locked) {
      var dx = Math.abs(t.clientX - startX), dy = Math.abs(t.clientY - startY);
      if (dy < LOCK_PX) return;
      // Mostly-horizontal drags (e.g. text selection) are left alone.
      if (dx > dy) { tracking = false; return; }
      locked = true;
      lastY = t.clientY;
      lastTime = e.timeStamp;
    }
    e.preventDefault();
    var delta = lastY - t.clientY;
    var dt = Math.max(e.timeStamp - lastTime, 1);
    velocity = 0.8 * (delta / dt) + 0.2 * velocity;
    lastX = t.clientX;
    lastY = t.clientY;
    lastTime = e.timeStamp;
    scrollPixels(delta);
  }, { capture: true, passive: false });

  function endTouch(e) {
    if (locked && e.type === 'touchend') {
      // A finger that stopped before lifting should not fling.
      if (e.timeStamp - lastTime > 100) velocity = 0;
      startFling();
    }
    tracking = false;
    locked = false;
  }
  document.addEventListener('touchend', endTouch, { capture: true, passive: true });
  document.addEventListener('touchcancel', endTouch, { capture: true, passive: true });
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
      e.preventDefault();
      e.stopPropagation();
      try { parent.postMessage({ type: 'devx:terminal-clipboard-image' }, '*'); } catch (err) {}
    }
  }, true);
})();
</script>`
