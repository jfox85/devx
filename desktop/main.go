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
	"mime/multipart"
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
	"github.com/jfox85/devx/session"
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
	// GUI-launched macOS apps inherit a minimal PATH (/usr/bin:/bin:/usr/sbin:
	// /sbin) rather than the user's shell PATH, so subprocesses the server spawns
	// (tmuxp, tmux, ttyd, direnv, ...) are not found and terminals fail to start.
	// Prepend common user/tool bin dirs so the embedded server behaves like
	// `devx web` launched from a terminal.
	augmentPATH()

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
	// Paste Image needs its own accelerator: the terminal runs in a cross-origin
	// iframe, so a plain Cmd+V goes straight to xterm and the SPA never sees the
	// paste event to pull the clipboard image from the native host. This menu
	// item fires regardless of iframe focus. Plain Cmd+V text paste is left to
	// xterm.
	devxMenu.AddText("Paste Image", keys.Combo("v", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:pasteImage"))
	devxMenu.AddSeparator()
	devxMenu.AddText("Toggle Artifacts", keys.Combo("a", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:toggleArtifacts"))
	devxMenu.AddText("Cycle Split", keys.Combo("o", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:cycleSplit"))
	// Cmd+Shift+V is reserved for Paste Image above; View Terminal Output uses U
	// ("oUtput") to avoid a duplicate accelerator that makes the shortcut
	// ambiguous.
	devxMenu.AddText("View Terminal Output", keys.Combo("u", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:viewTerminalOutput"))
	devxMenu.AddText("Insert Artifact", keys.Combo("i", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:insertArtifact"))
	devxMenu.AddText("New Text Artifact", keys.Combo("n", keys.CmdOrCtrlKey, keys.ShiftKey), emit("devx:newArtifact"))
	// A standard Edit menu is required so the WebView receives Cut/Copy/Paste/
	// SelectAll actions through the macOS responder chain. Without it, WKWebView
	// never gets the paste action, so plain Cmd+V (and tools like Raycast that
	// paste by simulating Cmd+V) produce no JS paste event in the app at all.
	appMenu.Append(menu.EditMenu())

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
		//
		// DisableWebViewDrop is required: our terminal is a cross-origin iframe, so
		// with WKWebView's own drag handling left enabled the webview rejects the
		// drop (it "floats back" to the source) before Wails' performDragOperation
		// runs. Disabling it makes the native handler accept every file drop and
		// forward the paths to OnFileDrop regardless of the DOM element underneath.
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
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

// DOM CustomEvent names the host dispatches into the SPA. These MUST stay in
// sync with the DESKTOP_EVENTS constants in web/app/src/lib/desktopBridge.js;
// there is no cross-language drift check, so the contract is anchored here and
// pinned by TestDropEventNames in main_test.go.
const (
	eventFileDrop         = "devx:desktop:filedrop"
	eventFileDropRejected = "devx:desktop:filedrop-rejected"
)

// droppedFile is the payload shape the SPA's filedrop handler decodes
// (fileFromBase64 in Terminal.svelte).
type droppedFile struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
}

// buildDropEvents partitions dropped paths into accepted image payloads and
// rejected file names, reading + validating each accepted file. It is the
// Wails-free, testable core of the drop bridge (it still does filesystem I/O
// and logging, but no Wails context or DOM); dispatchDroppedFiles wraps it to
// emit the events. accepted is nil unless at least one file was both a
// supported type and read successfully; rejected is nil unless at least one
// file was an unsupported type or failed to read.
func buildDropEvents(paths []string) (accepted []droppedFile, rejected []string) {
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
		accepted = append(accepted, droppedFile{
			Name: name,
			Type: mime,
			Data: base64.StdEncoding.EncodeToString(data),
		})
	}
	return accepted, rejected
}

// dispatchDroppedFiles reads dropped image files and forwards them to the SPA
// as a devx:desktop:filedrop CustomEvent carrying {name, type, data(base64)},
// plus a devx:desktop:filedrop-rejected event for any skipped files.
func (h *Host) dispatchDroppedFiles(paths []string) {
	if h.ctx == nil {
		return
	}
	accepted, rejected := buildDropEvents(paths)
	if len(accepted) > 0 {
		if encoded, err := json.Marshal(accepted); err == nil {
			h.dispatchEvent(eventFileDrop, string(encoded))
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
			h.dispatchEvent(eventFileDropRejected, string(encoded))
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

// ClipboardImage returns a clipboard image as base64, or "" when the clipboard
// holds no usable image. WKWebView often omits clipboard images from the DOM
// paste event, so the desktop paste path calls this native helper instead.
//
// Two clipboard shapes are handled because macOS apps populate the pasteboard
// differently: image editors and screenshots-to-clipboard put raw image data
// (coercible to «class PNGf»), while Finder copies and screenshot tools that
// save-then-copy put a file URL («class furl») pointing at the image file. The
// browser PWA's paste event surfaces both as a File; here we replicate that by
// trying raw image data first, then falling back to reading the referenced file.
func (h *Host) ClipboardImage() (string, error) {
	if goruntime.GOOS != "darwin" {
		return "", nil
	}
	if data := clipboardImageData(); len(data) > 0 {
		return base64.StdEncoding.EncodeToString(data), nil
	}
	if data := clipboardImageFile(); len(data) > 0 {
		return base64.StdEncoding.EncodeToString(data), nil
	}
	return "", nil // clipboard held no image; expected on text paste
}

// clipboardImageData coerces raw clipboard image data to PNG via AppleScript and
// returns the bytes, or nil when the clipboard has no coercible image data.
func clipboardImageData() []byte {
	tmp, err := os.CreateTemp("", "devx-clip-*.png")
	if err != nil {
		log.Printf("desktop: clipboard temp file: %v", err)
		return nil
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)
	// The «class PNGf» coercion fails (osascript still exits 0 via the on-error
	// branch) when the clipboard holds no raw image data, e.g. plain text or a
	// file reference; that is the expected no-image path, not an error.
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
end try`, tmpPath)
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		log.Printf("desktop: clipboard image osascript failed: %v", err)
		return nil
	}
	data, err := readCappedFile(tmpPath)
	if err != nil {
		log.Printf("desktop: reading clipboard temp file failed: %v", err)
		return nil
	}
	return data
}

// clipboardImageFile handles the «class furl» case: the clipboard references a
// file (Finder copy, save-then-copy screenshot tools). If it points at a
// supported image type, its bytes are read through the same hardened path as
// dropped files (O_NOFOLLOW, regular-file check, size cap).
func clipboardImageFile() []byte {
	out, err := exec.Command("osascript", "-e",
		`try
	return POSIX path of (the clipboard as «class furl»)
end try`).Output()
	if err != nil {
		return nil
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return nil
	}
	if _, ok := imagepolicy.ExtToMIME[strings.ToLower(filepath.Ext(path))]; !ok {
		return nil // not a supported image type
	}
	data, err := readDroppedImage(path)
	if err != nil {
		log.Printf("desktop: reading clipboard file %q failed: %v", path, err)
		return nil
	}
	return data
}

// readCappedFile reads up to the upload cap (plus one byte to detect overflow)
// and returns an error if the file exceeds it, matching the drop-path bound.
func readCappedFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDroppedFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDroppedFileBytes {
		return nil, fmt.Errorf("clipboard image exceeds %d byte cap", maxDroppedFileBytes)
	}
	return data, nil
}

// UploadImage saves a clipboard/dropped image to the session's upload dir and
// returns the upload handler's JSON response ({"path": ...}).
//
// This native binding exists because WKWebView's custom-scheme handler drops
// the HTTP body of POST requests issued by the WebView (fetch/XHR), so a
// multipart upload sent through the Wails asset-server proxy arrives with an
// empty body and the server rejects it with "failed to parse form". Routing the
// upload in-process to the private loopback server (with the real API token,
// which is never exposed to the WebView) sidesteps the WebView entirely while
// keeping the same /api/upload-image enforcement path the browser PWA uses.
func (h *Host) UploadImage(name, sessionName, dataB64 string) (string, error) {
	// Cheap pre-check to avoid a large decode allocation for an obviously oversize
	// payload. DecodedLen overestimates by up to 2 padding bytes, so add slop and
	// let the post-decode check below enforce the exact cap (so a payload right at
	// the cap isn't rejected by rounding).
	if base64.StdEncoding.DecodedLen(len(dataB64)) > maxDroppedFileBytes+3 {
		return "", fmt.Errorf("image exceeds %d byte cap", maxDroppedFileBytes)
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return "", fmt.Errorf("decode image: %w", err)
	}
	if len(data) > maxDroppedFileBytes {
		return "", fmt.Errorf("image exceeds %d byte cap", maxDroppedFileBytes)
	}
	if sessionName != "" && !session.IsValidSessionName(sessionName) {
		return "", fmt.Errorf("invalid session")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("image", name)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if sessionName != "" {
		if err := mw.WriteField("session", sessionName); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	url := "http://" + h.server.Addr() + "/api/upload-image"
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.server.Token())

	// Bound the in-process call so a wedged server can't hang the Wails binding.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("upload failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return string(respBody), nil
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
