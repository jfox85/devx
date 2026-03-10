package web

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/viper"
)

func registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("GET /api/sessions", handleListSessions)
	mux.HandleFunc("POST /api/sessions", handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions", handleDeleteSession)
	mux.HandleFunc("POST /api/sessions/{name}/flag", handleFlagSession)
	mux.HandleFunc("DELETE /api/sessions/{name}/flag", handleUnflagSession)
	// Session name passed as query param (?name=...) to avoid path-segment
	// splitting on session names that contain slashes.
	mux.HandleFunc("GET /api/windows", handleListWindows)
	mux.HandleFunc("GET /api/projects", handleListProjects)
	mux.HandleFunc("POST /api/switch-window", handleSwitchWindow)
	mux.HandleFunc("POST /api/send-keys", handleSendKeys)
	mux.HandleFunc("POST /api/refresh", handleRefreshTerminal)
	mux.HandleFunc("POST /api/upload-image", handleUploadImage)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // cannot change status after WriteHeader
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type loginRequest struct {
	Token string `json:"token"`
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	expectedToken := viper.GetString("web_secret_token")
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(expectedToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "devx_token",
		Value:    req.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   30 * 24 * 60 * 60, // 30 days — survive browser restarts
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sessionResponse is the JSON shape returned for each session.
type sessionResponse struct {
	Name           string            `json:"name"`
	Branch         string            `json:"branch"`
	ProjectAlias   string            `json:"project_alias,omitempty"`
	Ports          map[string]int    `json:"ports"`
	Routes         map[string]string `json:"routes"`
	ExternalRoutes map[string]string `json:"external_routes,omitempty"`
	AttentionFlag  bool              `json:"attention_flag"`
}

func buildSessionResponse(sess *session.Session) sessionResponse {
	externalDomain := viper.GetString("external_domain")
	externalRoutes := make(map[string]string)
	if externalDomain != "" {
		for svc := range sess.Ports {
			h := caddy.BuildExternalHostname(sess.Name, svc, sess.ProjectAlias, externalDomain)
			if h != "" {
				externalRoutes[svc] = h
			}
		}
	}
	return sessionResponse{
		Name:           sess.Name,
		Branch:         sess.Branch,
		ProjectAlias:   sess.ProjectAlias,
		Ports:          sess.Ports,
		Routes:         sess.Routes,
		ExternalRoutes: externalRoutes,
		AttentionFlag:  sess.AttentionFlag,
	}
}

func handleListSessions(w http.ResponseWriter, r *http.Request) {
	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	sessions := make([]sessionResponse, 0, len(store.Sessions))
	for _, sess := range store.Sessions {
		sessions = append(sessions, buildSessionResponse(sess))
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

type createSessionRequest struct {
	Name    string `json:"name"`
	Project string `json:"project"`
}

func handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if !session.IsValidSessionName(req.Name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session name"})
		return
	}

	// Use -- to separate flags from the positional session name, preventing
	// a name starting with "-" from being misinterpreted as a flag by Cobra.
	args := []string{"session", "create"}
	if req.Project != "" {
		args = append(args, "--project", req.Project)
	}
	args = append(args, "--", req.Name)
	if err := runSelf(args...); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	store, err := session.LoadSessions()
	if err != nil || store == nil {
		writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
		return
	}
	if sess, ok := store.Sessions[req.Name]; ok {
		writeJSON(w, http.StatusCreated, buildSessionResponse(sess))
	} else {
		writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
	}
}

func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	// Pass --force to skip the interactive y/N prompt. runSelf has no stdin
	// connected, so Scanln blocks forever without it.
	// Use -- to separate flags from the positional name argument.
	if err := runSelf("session", "rm", "--force", "--", name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleFlagSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := session.SetAttentionFlag(name, "manual"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleUnflagSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := session.ClearAttentionFlag(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveWebSession returns "<name>-web" if the web-grouped session exists,
// otherwise returns name. All web-facing tmux operations should target the
// web session so that active-window state and client sizes are correct for
// the browser viewport, not a terminal client that may be attached to the
// base session.
func resolveWebSession(name string) string {
	if exec.Command("tmux", "has-session", "-t", name+"-web").Run() == nil {
		return name + "-web"
	}
	return name
}

// requireValidSession validates that name is a legal devx session name and
// returns a 400 response if it is not. Returns true if the name is valid.
func requireValidSession(w http.ResponseWriter, name string) bool {
	if !session.IsValidSessionName(name) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session name"})
		return false
	}
	return true
}

// windowInfo is the JSON shape for a single tmux window.
type windowInfo struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// handleListWindows returns the tmux windows for the session given by ?name=.
// The query-param approach avoids Go ServeMux splitting session names on "/".
//
// We query the web-grouped session (<name>-web) so that #{window_active}
// reflects the window currently visible in the browser, not the terminal
// client's current window.
func handleListWindows(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}

	out, err := exec.Command("tmux", "list-windows", "-t", resolveWebSession(name), "-F",
		"#{window_index} #{window_name} #{window_active}").Output()
	if err != nil {
		// tmux session not running — return empty list rather than an error.
		writeJSON(w, http.StatusOK, map[string]any{"windows": []windowInfo{}})
		return
	}

	var windows []windowInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}
		idx, _ := strconv.Atoi(parts[0])
		windows = append(windows, windowInfo{
			Index:  idx,
			Name:   parts[1],
			Active: parts[2] == "1",
		})
	}
	if windows == nil {
		windows = []windowInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"windows": windows})
}

// handleListProjects returns the sorted list of project aliases from the registry.
func handleListProjects(w http.ResponseWriter, r *http.Request) {
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	aliases := make([]string, 0, len(registry.Projects))
	for alias := range registry.Projects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	writeJSON(w, http.StatusOK, map[string]any{"projects": aliases})
}

// handleSwitchWindow runs `tmux select-window -t session:index`, which switches
// the active window for ALL clients attached to the session (including the iframe).
//
// We target the web-grouped session (<name>-web) so the switch is visible in
// the browser. Because it is a grouped session, the base session follows too.
func handleSwitchWindow(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	window := r.URL.Query().Get("window")
	if name == "" || window == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and window required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	if _, err := strconv.Atoi(window); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "window must be a numeric index"})
		return
	}
	if err := exec.Command("tmux", "select-window", "-t", resolveWebSession(name)+":"+window).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRefreshTerminal forces the tmux window to match the browser viewport.
//
// In tmux grouped sessions the shared window can get stuck at the base
// session's stored size (e.g. 49x39) even after the browser's FitAddon sends
// the correct dimensions via ioctl(TIOCSWINSZ). Explicitly reading the
// client's current dimensions and calling resize-window fixes the dots/padding
// issue without relying on ioctl propagation through tmux's grouped-session
// size-recalculation logic.
func handleRefreshTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	webSess := resolveWebSession(name)
	_ = exec.Command("tmux", "refresh-client", "-t", webSess).Run()
	resizeWindowToClient(webSess)
	w.WriteHeader(http.StatusNoContent)
}

// resizeWindowToClient reads the most-recently-active connected client's
// dimensions for the given tmux target and explicitly resizes the window to
// match.  This works around a tmux grouped-session behaviour where the base
// session's stored size acts as a constraint, preventing ioctl TIOCSWINSZ from
// resizing the window.
//
// We use the most recently active client ("latest") rather than the largest so
// that whichever device you're currently using determines the terminal size.
// If the session is open in a large desktop browser and a small phone, the
// phone shouldn't inherit the desktop's dots-filled viewport just because the
// desktop window is bigger.
//
// Note: in tmux grouped sessions, windows are shared. resize-window changes
// the shared window size, so a terminal client also attached to the base
// session will momentarily see the web viewport's dimensions. With
// window-size=latest this self-corrects on the next terminal keystroke (tmux
// re-evaluates the most recently active client), so the tradeoff is acceptable
// compared to the alternative of leaving the web session stuck at the base
// session's stale size.
func resizeWindowToClient(target string) {
	// client_activity is a Unix timestamp; pick the client with the highest value.
	out, err := exec.Command("tmux", "list-clients", "-t", target, "-F", "#{client_activity} #{client_width} #{client_height}").Output()
	if err != nil || len(out) == 0 {
		return
	}
	latestActivity := -1 // sentinel so a client with activity=0 is still selected
	var latestW, latestH int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		act, err0 := strconv.Atoi(parts[0])
		cw, err1 := strconv.Atoi(parts[1])
		ch, err2 := strconv.Atoi(parts[2])
		if err0 != nil || err1 != nil || err2 != nil || cw <= 0 || ch <= 0 {
			continue
		}
		if act > latestActivity {
			latestActivity = act
			latestW = cw
			latestH = ch
		}
	}
	if latestW <= 0 || latestH <= 0 {
		return
	}
	_ = exec.Command("tmux", "resize-window", "-t", target,
		"-x", strconv.Itoa(latestW),
		"-y", strconv.Itoa(latestH)).Run()
}

// handleSendKeys runs `tmux send-keys -t session key`, delivering the key to the
// session's current window/pane regardless of which tmux client is active.
func handleSendKeys(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	keys := r.URL.Query().Get("keys")
	if name == "" || keys == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and keys required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	// Split on whitespace so callers can send multiple keystrokes (e.g. "C-b C-b").
	keyList := strings.Fields(keys)
	args := append([]string{"send-keys", "-t", name}, keyList...)
	if err := exec.Command("tmux", args...).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var allowedImageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// handleUploadImage accepts a multipart image upload, saves it to
// ~/.devx/uploads/{hex}.ext, and returns the absolute path as JSON.
func handleUploadImage(w http.ResponseWriter, r *http.Request) {
	// Cap the raw request body to 20 MB before parsing to prevent disk exhaustion.
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to parse form"})
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image field required"})
		return
	}
	defer file.Close()

	// Always sniff magic bytes to determine MIME type — never trust the
	// client-supplied Content-Type header, which is trivially spoofable.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	ct := http.DetectContentType(buf[:n])
	// Seek back to start so io.Copy gets the full file contents.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "seek failed"})
		return
	}

	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid content type"})
		return
	}

	ext, ok := allowedImageTypes[mediaType]
	if !ok {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "unsupported image type: " + mediaType})
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot find home dir"})
		return
	}

	uploadDir := filepath.Join(home, ".devx", "uploads")
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create upload dir"})
		return
	}

	var randBytes [16]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "random generation failed"})
		return
	}
	filename := hex.EncodeToString(randBytes[:]) + ext
	destPath := filepath.Join(uploadDir, filename)

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot create file"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"path": destPath})
}

// runSelf re-invokes the devx binary with the given args.
// This reuses all existing CLI logic without duplicating it.
// TMUX and TMUX_PANE are stripped so that commands like "session create"
// don't detect they're inside tmux and skip launching the session.
func runSelf(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") {
			env = append(env, e)
		}
	}
	var stderr bytes.Buffer
	cmd := exec.Command(self, args...)
	cmd.Env = env
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
		return err
	}
	return nil
}
