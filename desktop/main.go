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
	"encoding/base64"
	"encoding/json"
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

	"github.com/jfox85/devx/web"
	"github.com/jfox85/devx/web/imagepolicy"
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
		// Wails' WebView swallows OS file drops before the DOM sees them. Enabling
		// the file-drop bridge makes OnFileDrop fire with absolute paths, which we
		// forward into the existing web upload flow (see host.startup).
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
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
	// Bridge native file drops into the web upload flow. Wails delivers absolute
	// paths; we read each image and hand the bytes to the SPA as a DOM
	// CustomEvent (same mechanism as the menu accelerators), so the existing
	// drag/drop upload code runs unchanged inside the WebView.
	wailsruntime.OnFileDrop(ctx, func(_, _ int, paths []string) {
		h.dispatchDroppedFiles(paths)
	})
}

// maxDroppedFileBytes mirrors the server's upload cap so we fail fast in the
// host instead of base64-encoding a file the API will reject. The accepted
// extensions/types come from the same imagepolicy source of truth as the HTTP
// upload handler, so the bridge can never drift from what the API accepts.
const maxDroppedFileBytes = imagepolicy.MaxUploadBytes

// readDroppedImage reads a dropped file's bytes after verifying it is a regular
// file and enforcing the size cap while reading. O_NOFOLLOW rejects a dropped
// path whose final component is a symlink, so a ".png" symlink pointing at an
// arbitrary readable file is refused rather than silently dereferenced. Opening
// once and stat-ing the resulting fd (not the path) closes the Stat→ReadFile
// TOCTOU window. Note: O_NOFOLLOW only guards the leaf; a symlinked *parent*
// directory component is still resolved by the kernel. That is acceptable here
// because the user chose the dragged path; we only defend against a swapped or
// symlinked target file, not against the user dragging from a symlinked dir.
func readDroppedImage(p string) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (mode %v)", info.Mode())
	}
	if info.Size() > maxDroppedFileBytes {
		return nil, fmt.Errorf("file exceeds %d byte cap (%d)", maxDroppedFileBytes, info.Size())
	}
	// Cap the read at one byte over the limit so a file that grows between Stat
	// and read (or whose reported size lies) cannot blow past the cap.
	data, err := io.ReadAll(io.LimitReader(f, maxDroppedFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDroppedFileBytes {
		return nil, fmt.Errorf("file exceeds %d byte cap during read", maxDroppedFileBytes)
	}
	return data, nil
}

// dispatchDroppedFiles reads dropped image files and forwards them to the SPA
// as a devx:desktop:filedrop CustomEvent carrying {name, type, data(base64)}.
func (h *Host) dispatchDroppedFiles(paths []string) {
	if h.ctx == nil {
		return
	}
	type payload struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Data string `json:"data"`
	}
	var files []payload
	var rejected []string
	for _, p := range paths {
		name := filepath.Base(p)
		mime, ok := imagepolicy.ExtToMIME[strings.ToLower(filepath.Ext(p))]
		if !ok {
			rejected = append(rejected, name)
			continue // not a supported image type
		}
		data, err := readDroppedImage(p)
		if err != nil {
			// Skip unreadable/symlinked/oversize drops, but log so a regressed
			// bridge or permission failure is debuggable instead of a silent no-op.
			log.Printf("desktop: skipping dropped file %q: %v", p, err)
			rejected = append(rejected, name)
			continue
		}
		files = append(files, payload{
			Name: name,
			Type: mime,
			Data: base64.StdEncoding.EncodeToString(data),
		})
	}
	if len(files) > 0 {
		if encoded, err := json.Marshal(files); err == nil {
			h.dispatchEvent("devx:desktop:filedrop", string(encoded))
		} else {
			log.Printf("desktop: marshaling dropped files failed: %v", err)
		}
	}
	// Surface host-side rejections (oversize, unreadable, unsupported type) so the
	// desktop drop shows feedback instead of silently dropping files, matching the
	// web upload error UX. Notify whenever anything was rejected — including a
	// partial multi-drop where some files uploaded — so discarded files are never
	// silent. The accepted files still upload via the filedrop event above.
	if len(rejected) > 0 {
		if encoded, err := json.Marshal(rejected); err == nil {
			h.dispatchEvent("devx:desktop:filedrop-rejected", string(encoded))
		} else {
			log.Printf("desktop: marshaling rejected drops failed: %v", err)
		}
	}
}

// dispatchEvent dispatches a DOM CustomEvent into the SPA with the given JSON
// detail. detailJSON must be a valid JSON value (it is embedded as a raw JS
// literal, not a quoted string); callers pass json.Marshal output.
func (h *Host) dispatchEvent(name, detailJSON string) {
	if h.ctx == nil {
		return
	}
	wailsruntime.WindowExecJS(h.ctx, fmt.Sprintf(
		`window.dispatchEvent(new CustomEvent(%q,{detail:%s}))`,
		name, detailJSON,
	))
}

// ClipboardImage returns a base64-encoded PNG of the current clipboard image,
// or an empty string if the clipboard holds no image. The WebView's DOM paste
// event does not reliably expose clipboard images on macOS WKWebView, so the
// frontend calls this binding on Cmd/Ctrl+V and routes the result through the
// same upload flow as drag/drop.
func (h *Host) ClipboardImage() (string, error) {
	if goruntime.GOOS != "darwin" {
		return "", nil
	}
	tmp, err := os.CreateTemp("", "devx-clip-*.png")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)
	// Write any clipboard image to a PNG file via AppleScript. If the clipboard
	// has no image data, the «class PNGf» coercion fails and we return empty.
	script := fmt.Sprintf(`try
	set png to (the clipboard as «class PNGf»)
	set f to open for access POSIX file %q with write permission
	set eof f to 0
	write png to f
	close access f
on error
	try
		close access f
	end try
	return ""
end try`, tmpPath)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		// A non-zero exit here is an osascript execution/permission failure, not
		// "clipboard has no image" (that path returns "" with exit 0). Log it so a
		// regressed clipboard bridge is debuggable, but still fail soft for paste.
		log.Printf("desktop: clipboard image osascript failed: %v", err)
		return "", nil
	}
	// Bound the read like the drop path: a misbehaving app could leave a huge
	// image on the clipboard, and the upload API rejects anything over the cap
	// anyway, so fail fast instead of buffering + base64-encoding it.
	clip, err := os.Open(tmpPath)
	if err != nil {
		log.Printf("desktop: opening clipboard temp file failed: %v", err)
		return "", nil
	}
	defer clip.Close()
	data, err := io.ReadAll(io.LimitReader(clip, maxDroppedFileBytes+1))
	if err != nil {
		log.Printf("desktop: reading clipboard temp file failed: %v", err)
		return "", nil
	}
	if len(data) == 0 {
		return "", nil // clipboard held no image; expected on text paste
	}
	if len(data) > maxDroppedFileBytes {
		log.Printf("desktop: clipboard image exceeds %d byte cap", maxDroppedFileBytes)
		return "", nil
	}
	return base64.StdEncoding.EncodeToString(data), nil
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
