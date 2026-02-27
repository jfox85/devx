package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"

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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleUnflagSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := session.ClearAttentionFlag(name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
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
