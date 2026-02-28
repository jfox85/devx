package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/session"
	"github.com/spf13/viper"
)

func registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("GET /api/sessions", handleListSessions)
	mux.HandleFunc("POST /api/sessions", handleCreateSession)
	mux.HandleFunc("DELETE /api/sessions/{name}", handleDeleteSession)
	mux.HandleFunc("POST /api/sessions/{name}/flag", handleFlagSession)
	mux.HandleFunc("DELETE /api/sessions/{name}/flag", handleUnflagSession)
	// Session name passed as query param (?name=...) to avoid path-segment
	// splitting on session names that contain slashes.
	mux.HandleFunc("GET /api/windows", handleListWindows)
	mux.HandleFunc("POST /api/switch-window", handleSwitchWindow)
	mux.HandleFunc("POST /api/send-keys", handleSendKeys)
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
	if req.Token != expectedToken {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "devx_token",
		Value:    req.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
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

	args := []string{"session", "create", req.Name, "--no-tmux"}
	if req.Project != "" {
		args = append(args, "--project", req.Project)
	}
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
	name := r.PathValue("name")
	if err := runSelf("session", "rm", name); err != nil {
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

// windowInfo is the JSON shape for a single tmux window.
type windowInfo struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// handleListWindows returns the tmux windows for the session given by ?name=.
// The query-param approach avoids Go ServeMux splitting session names on "/".
func handleListWindows(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}

	out, err := exec.Command("tmux", "list-windows", "-t", name, "-F",
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

// handleSwitchWindow runs `tmux select-window -t session:index`, which switches
// the active window for ALL clients attached to the session (including the iframe).
func handleSwitchWindow(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	window := r.URL.Query().Get("window")
	if name == "" || window == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and window required"})
		return
	}
	if err := exec.Command("tmux", "select-window", "-t", name+":"+window).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if err := exec.Command("tmux", "send-keys", "-t", name, keys).Run(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runSelf re-invokes the devx binary with the given args.
// This reuses all existing CLI logic without duplicating it.
func runSelf(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}
	var stderr bytes.Buffer
	cmd := exec.Command(self, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes()))
		}
		return err
	}
	return nil
}
