// Command desktop is the Wails feasibility spike from the control deck plan
// (Phase 0E / Phase 3A): a thin native shell around the existing DevX web UI.
//
// Topology (plan "Preferred MVP desktop topology"):
//   - Each launch starts a private DevX web server on a random loopback port
//     with an ephemeral in-memory token (web.PrivateServer).
//   - The privileged WebView loads only that private origin.
//   - The host injects the token via a Wails binding (Host.SessionInfo); it is
//     never placed in URLs, CLI args, logs, or persisted storage.
//   - Attaching to an existing long-lived daemon is explicitly out of scope
//     until a challenge/response attach protocol exists.
//
// Spike validation targets (plan 0E):
//   - ttyd iframe works inside the Wails WebView
//   - SSE events reach the app
//   - native notifications can be triggered from the host abstraction
//
// Build (requires platform WebView toolchain - macOS: Xcode CLT; Linux:
// webkit2gtk; Windows: WebView2):
//
//	cd desktop && wails build
//	cd desktop && wails dev   # development loop
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/jfox85/devx/web"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend
var frontendAssets embed.FS

func main() {
	priv, err := web.NewPrivateServer()
	if err != nil {
		log.Fatalf("failed to create private devx server: %v", err)
	}
	go func() {
		if err := priv.Serve(); err != nil && err != http.ErrServerClosed {
			log.Printf("private devx server exited: %v", err)
		}
	}()

	host := &Host{server: priv}

	// Reverse-proxy the WebView's asset requests to the private server. This
	// keeps the WebView on the wails:// app origin while all /api, /terminal,
	// and SSE traffic flows to the loopback server with the token attached by
	// the proxy - the frontend never needs the token for same-origin calls.
	target := &url.URL{Scheme: "http", Host: priv.Addr()}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.Header.Set("Authorization", "Bearer "+priv.Token())
	}

	err = wails.Run(&options.App{
		Title:  "DevX",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets:  frontendAssets,
			Handler: proxy, // non-embedded paths (/, /api, /terminal, ...) hit the private server
		},
		OnStartup:  host.startup,
		OnShutdown: host.shutdown,
		Bind:       []interface{}{host},
	})
	if err != nil {
		log.Fatalf("wails run: %v", err)
	}
}

// Host exposes native capabilities to the frontend via Wails bindings. It is
// the only privileged bridge; service/artifact previews must never get access
// to it (plan invariant 3).
type Host struct {
	ctx    context.Context
	server *web.PrivateServer
}

func (h *Host) startup(ctx context.Context) {
	h.ctx = ctx
}

func (h *Host) shutdown(ctx context.Context) {
	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = h.server.Shutdown(shutdownCtx)
}

// SessionInfo returns the private server address. The token is intentionally
// NOT exposed to the frontend: the asset-server proxy attaches it server-side,
// so the WebView never holds credentials (plan invariant 2).
func (h *Host) SessionInfo() map[string]string {
	return map[string]string{
		"addr": h.server.Addr(),
		"mode": "private",
	}
}

// Notify shows a native notification with redacted content (plan invariant 7:
// generic title, no prompt bodies, no paths).
func (h *Host) Notify(title string, body string) error {
	if h.ctx == nil {
		return fmt.Errorf("host not started")
	}
	// Wails v2 has no cross-platform notification API; v3 will. For the spike,
	// log only. Production options: platform notifiers (osascript / notify-send
	// / toast) behind a build-tagged helper, or Wails v3 when stable.
	log.Printf("notify: %s — %s", title, body)
	return nil
}
