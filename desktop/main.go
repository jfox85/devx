// Command desktop is the Wails feasibility spike from the control deck plan
// (Phase 0E / Phase 3A): a thin native shell around the existing DevX web UI.
//
// Topology (plan "Preferred MVP desktop topology"):
//   - Each launch starts a private DevX web server on a random loopback port
//     with an ephemeral in-memory token (web.PrivateServer).
//   - The privileged WebView loads only that private origin.
//   - The shell injects the token server-side in its internal reverse proxy;
//     it is never exposed to the WebView, URLs, CLI args, logs, or persisted
//     storage.
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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	_ "embed"

	"github.com/jfox85/devx/session"
	"github.com/jfox85/devx/web"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/appicon.png
var appIcon []byte

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
	appMenu := menu.NewMenu()
	if goruntime.GOOS == "darwin" {
		// Wails replaces the whole application menu when Menu is supplied. Keep the
		// standard macOS app menu and add our Cmd+V bridge below so paste shortcuts
		// are handled by the trusted top-level shell, not by arbitrary WebKit frames.
		appMenu.Append(menu.AppMenu())
	}
	devxMenu := appMenu.AddSubmenu("DevX")
	emit := func(event string) func(*menu.CallbackData) {
		return func(_ *menu.CallbackData) {
			if host.ctx != nil {
				wailsruntime.WindowExecJS(host.ctx, fmt.Sprintf(`window.dispatchEvent(new CustomEvent(%q))`, event))
			}
		}
	}
	devxMenu.AddText("Quick Switch Session", keys.CmdOrCtrl("p"), emit("devx:quickSwitcher"))
	devxMenu.AddText("Compose Prompt", keys.CmdOrCtrl("k"), emit("devx:toggleComposer"))
	devxMenu.AddText("Focus Terminal", keys.Combo("t", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:focusTerminal"))
	devxMenu.AddText("Focus Session List", keys.Combo("s", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:focusSessionList"))
	devxMenu.AddText("New Session", keys.Combo("c", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:newSession"))
	devxMenu.AddText("Paste", keys.CmdOrCtrl("v"), emit("devx:nativePaste"))
	devxMenu.AddSeparator()
	devxMenu.AddText("Toggle Artifacts", keys.Combo("a", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:toggleArtifacts"))
	devxMenu.AddText("Cycle Split", keys.Combo("o", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:cycleSplit"))
	devxMenu.AddText("View Terminal Output", keys.Combo("v", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:viewTerminalOutput"))
	devxMenu.AddText("Insert Artifact", keys.Combo("i", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:insertArtifact"))
	devxMenu.AddText("New Text Artifact", keys.Combo("n", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:newArtifact"))
	// Keep the SPA loaded from the Wails asset server (no external-link landing
	// page), but let terminal iframes go directly to the private loopback origin
	// so ttyd WebSockets do not traverse Wails' asset-server proxy.
	privateURL := "http://" + priv.Addr()
	target := &url.URL{Scheme: "http", Host: priv.Addr()}
	proxy := httputil.NewSingleHostReverseProxy(target)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.Header.Set("Authorization", "Bearer "+priv.Token())
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		injection := fmt.Sprintf(`<script>
localStorage.setItem('devx_authed','1');
window.__DEVX_DESKTOP = { terminalBase: %q, terminalToken: %q };
</script>`, privateURL, priv.TerminalBootstrapToken())
		body = bytes.Replace(body, []byte("<head>"), []byte("<head>"+injection), 1)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
		return nil
	}

	err = wails.Run(&options.App{
		Title:  "DevX",
		Width:  1280,
		Height: 800,
		Menu:   appMenu,
		AssetServer: &assetserver.Options{
			// API/SSE/static assets are proxied with bearer auth. Terminal iframes
			// use window.__DEVX_DESKTOP to connect directly to privateURL.
			Handler: proxy,
		},
		OnStartup:  host.startup,
		OnShutdown: host.shutdown,
		Bind:       []interface{}{host},
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "DevX",
				Message: "Local development control deck",
				Icon:    appIcon,
			},
		},
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

// OpenExternal opens service/artifact URLs in the user's default browser. Wails
// WebViews do not behave like normal browser tabs for target=_blank links.
func (h *Host) OpenExternal(url string) error {
	if h.ctx == nil {
		return fmt.Errorf("host not started")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("unsupported external URL")
	}
	wailsruntime.BrowserOpenURL(h.ctx, url)
	return nil
}

// Notify shows a native notification. Flag notifications intentionally include
// the session name and caller-supplied reason because otherwise desktop
// attention is too vague to act on.
func (h *Host) Notify(title string, body string) error {
	if h.ctx == nil {
		return fmt.Errorf("host not started")
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		title = "DevX"
	}
	if len(body) > 240 {
		body = body[:240] + "…"
	}
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("osascript", "-e", fmt.Sprintf("display notification %s with title %s", strconv.Quote(body), strconv.Quote(title))).Run()
	case "linux":
		return exec.Command("notify-send", title, body).Run()
	default:
		log.Printf("notify: %s — %s", title, body)
		return nil
	}
}

// ClipboardImageDataURL returns the current clipboard image as a PNG data URL,
// or an empty string when the clipboard has no image. It gives the frontend a
// same-origin paste path for the desktop terminal iframe, whose direct loopback
// origin intentionally prevents JS from installing an in-iframe paste listener.
func (h *Host) ClipboardImageDataURL() (string, error) {
	data, err := readClipboardPNG()
	if err != nil || len(data) == 0 {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data), nil
}

func readClipboardPNG() ([]byte, error) {
	if goruntime.GOOS != "darwin" {
		return nil, nil
	}
	tmp, err := os.CreateTemp("", "devx-clipboard-*.png")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	args := []string{
		"-e", fmt.Sprintf("set theFile to POSIX file %s", strconv.Quote(path)),
		"-e", "set img to the clipboard as «class PNGf»",
		"-e", "set fp to open for access theFile with write permission",
		"-e", "set eof fp to 0",
		"-e", "write img to fp",
		"-e", "close access fp",
	}
	if err := exec.Command("/usr/bin/osascript", args...).Run(); err != nil {
		// No image on the clipboard is not an application error; the frontend will
		// fall back to text paste.
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// PasteClipboardImage saves the current clipboard image to the same per-session
// upload directory used by the web multipart endpoint and returns the path that
// should be inserted into the terminal.
func (h *Host) PasteClipboardImage(sessionName string) (string, error) {
	data, err := readClipboardPNG()
	if err != nil || len(data) == 0 {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	uploadSubdir := "uploads"
	if sessionName != "" {
		uploadSubdir = filepath.Join("uploads", sessionName)
	}
	uploadDir := filepath.Join(home, ".devx", uploadSubdir)
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		return "", err
	}
	var randBytes [16]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return "", err
	}
	destPath := filepath.Join(uploadDir, hex.EncodeToString(randBytes[:])+".png")
	if err := os.WriteFile(destPath, data, 0o600); err != nil {
		return "", err
	}
	resultPath := destPath
	if sessionName != "" {
		store, _ := session.LoadSessions()
		if store != nil {
			if sess, ok := store.Sessions[sessionName]; ok && sess.Target.Gatepost.Enabled {
				resultPath = filepath.Join("/root/.devx/uploads", filepath.Base(destPath))
			}
		}
	}
	return resultPath, nil
}

// ClipboardText returns the current clipboard text. It backs the same native
// paste bridge as ClipboardImageDataURL so Cmd+V remains useful when the custom
// image bridge replaces macOS' global WebKit Edit menu paste path.
func (h *Host) ClipboardText() (string, error) {
	if h.ctx == nil {
		return "", nil
	}
	return wailsruntime.ClipboardGetText(h.ctx)
}
