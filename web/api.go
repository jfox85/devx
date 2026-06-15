package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/viper"
)

func registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", handleHealth)
	mux.HandleFunc("POST /api/login", handleLogin)
	mux.HandleFunc("GET /api/sessions", handleListSessions)
	mux.HandleFunc("GET /api/sessions/stale", handleStaleSessions)
	mux.HandleFunc("POST /api/sessions", handleCreateSession)
	mux.HandleFunc("GET /api/sessions/create-status", handleSessionCreateStatus)
	mux.HandleFunc("DELETE /api/sessions", handleDeleteSession)
	mux.HandleFunc("DELETE /api/sessions/stale-clean", handleDeleteStaleCleanSessions)
	// Session name passed as query param (?name=...) to avoid path-segment
	// splitting on session names that contain slashes.
	mux.HandleFunc("GET /api/windows", handleListWindows)
	mux.HandleFunc("GET /api/active-pane", handleActivePane)
	mux.HandleFunc("GET /api/pane-content", handlePaneContent)
	mux.HandleFunc("GET /api/pane-content.txt", handlePaneContentText)
	mux.HandleFunc("GET /api/pane-content/view", handlePaneContentView)
	mux.HandleFunc("GET /api/projects", handleListProjects)
	mux.HandleFunc("GET /api/settings", handleSettings)
	mux.HandleFunc("POST /api/switch-window", handleSwitchWindow)
	mux.HandleFunc("POST /api/send-keys", handleSendKeys)
	mux.HandleFunc("POST /api/refresh", handleRefreshTerminal)
	mux.HandleFunc("POST /api/upload-image", handleUploadImage)
	mux.HandleFunc("POST /api/sessions/rename", handleRenameSession)
	mux.HandleFunc("POST /api/sessions/color", handleColorSession)
	mux.HandleFunc("GET /api/sessions/review", handleGetSessionReview)
	mux.HandleFunc("POST /api/sessions/review", handleReviewSession)
	mux.HandleFunc("POST /api/sessions/reviewed", handleMarkSessionReviewed)
	mux.HandleFunc("GET /api/gatepost/logs", handleGatepostLogsRedirect)
	// Reverse-proxy the per-session Gatepost Logs UI so it is reachable wherever
	// the devx web UI is (Caddy / Cloudflare tunnel), with the token injected
	// server-side. Catch-all so branch-style session names (with slashes) work.
	mux.HandleFunc(gatepostLogsPathPrefix, handleGatepostLogsProxy)
	// Serve uploaded files — auth enforced via /uploads/ prefix in authMiddleware.
	mux.HandleFunc("GET /uploads/", handleServeUpload)
}

var execTmuxOutput = func(args ...string) ([]byte, error) {
	return exec.Command("tmux", args...).Output()
}

var execTmuxRun = func(args ...string) error {
	return exec.Command("tmux", args...).Run()
}

var markSessionReviewed = session.MarkReviewed

const sessionListCacheTTL = 10 * time.Second
const fullStaleScanCacheTTL = 30 * time.Second

var sessionListCache = struct {
	sync.Mutex
	key     string
	expires time.Time
	payload map[string]any
}{}

var fullStaleScanCache = struct {
	sync.Mutex
	key     string
	expires time.Time
	payload map[string]any
}{}

func getCachedSessionList(key string) (map[string]any, bool) {
	sessionListCache.Lock()
	defer sessionListCache.Unlock()
	if key == sessionListCache.key && time.Now().Before(sessionListCache.expires) && sessionListCache.payload != nil {
		return sessionListCache.payload, true
	}
	return nil, false
}

func setCachedSessionList(key string, payload map[string]any) {
	sessionListCache.Lock()
	defer sessionListCache.Unlock()
	sessionListCache.key = key
	sessionListCache.payload = payload
	sessionListCache.expires = time.Now().Add(sessionListCacheTTL)
}

func invalidateSessionListCache() {
	sessionListCache.Lock()
	sessionListCache.key = ""
	sessionListCache.expires = time.Time{}
	sessionListCache.payload = nil
	sessionListCache.Unlock()

	fullStaleScanCache.Lock()
	fullStaleScanCache.key = ""
	fullStaleScanCache.expires = time.Time{}
	fullStaleScanCache.payload = nil
	fullStaleScanCache.Unlock()
}

func getCachedFullStaleScan(key string) (map[string]any, bool) {
	fullStaleScanCache.Lock()
	defer fullStaleScanCache.Unlock()
	if key == fullStaleScanCache.key && time.Now().Before(fullStaleScanCache.expires) && fullStaleScanCache.payload != nil {
		return fullStaleScanCache.payload, true
	}
	return nil, false
}

func computeCachedFullStaleScan(key string, compute func() (map[string]any, error)) (map[string]any, error) {
	fullStaleScanCache.Lock()
	defer fullStaleScanCache.Unlock()
	if key == fullStaleScanCache.key && time.Now().Before(fullStaleScanCache.expires) && fullStaleScanCache.payload != nil {
		return fullStaleScanCache.payload, nil
	}
	payload, err := compute()
	if err != nil {
		return nil, err
	}
	fullStaleScanCache.key = key
	fullStaleScanCache.payload = payload
	fullStaleScanCache.expires = time.Now().Add(fullStaleScanCacheTTL)
	return payload, nil
}

var paneContentViewTmpl = template.Must(template.New("pane-content-view").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <title>devx pane output</title>
  <style>
    body{margin:0;background:#020617;color:#e5eefc;font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
    header{position:sticky;top:0;background:#0f172a;border-bottom:1px solid #1e293b;padding:12px 14px}
    h1{margin:0;font-size:18px}
    p{margin:4px 0 0;color:#94a3b8;font-size:12px}
    pre{margin:0;padding:14px;font-size:14px;line-height:1.45;white-space:pre;overflow:auto;-webkit-overflow-scrolling:touch}
  </style>
</head>
<body>
  <header>
    <h1>Full Output</h1>
    <p>Session: {{.Name}} · Target: {{.Target}}</p>
  </header>
  <pre id="pane-output">{{.Content}}</pre>
  <script>(function(){function scrollToBottom(){window.scrollTo(0, document.documentElement.scrollHeight || document.body.scrollHeight);}if(document.readyState==='complete'){scrollToBottom();}else{window.addEventListener('load', scrollToBottom, {once:true});}requestAnimationFrame(scrollToBottom);setTimeout(scrollToBottom, 50);})();</script>
</body>
</html>`))

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

func handleSettings(w http.ResponseWriter, r *http.Request) {
	defaultTarget := viper.GetString("target")
	if defaultTarget == "" {
		defaultTarget = "host"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact_trigger_key":   viper.GetString("artifact_trigger_key"),
		"default_session_target": defaultTarget,
	})
}

func defaultStaleDays() int {
	days := viper.GetInt("stale_sessions.threshold_days")
	if days <= 0 || days > session.MaxStaleThresholdDays {
		days = 14
	}
	return days
}

func parseStaleDays(raw string) (int, error) {
	if raw == "" {
		return defaultStaleDays(), nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("days must be a positive integer")
	}
	if _, err := session.StaleThresholdDuration(days); err != nil {
		return 0, err
	}
	return days, nil
}

func webStaleThreshold(days int) (time.Duration, error) {
	if days <= 0 {
		days = defaultStaleDays()
	}
	return session.StaleThresholdDuration(days)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	expectedToken := viper.GetString("web_secret_token")
	if expectedToken == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "web auth is not configured"})
		return
	}
	if req.Token == "" || subtle.ConstantTimeCompare([]byte(req.Token), []byte(expectedToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "devx_token",
		Value:    req.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Secure when the login arrived over HTTPS (direct TLS, or via a trusted
		// proxy that set X-Forwarded-Proto). Plain-HTTP localhost logins still
		// work: browsers permit httpOnly cookies on localhost without Secure.
		Secure: requestIsHTTPS(r),
		MaxAge: 30 * 24 * 60 * 60, // 30 days — survive browser restarts
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sessionResponse is the JSON shape returned for each session.
type gatepostResponse struct {
	Enabled             bool     `json:"enabled"`
	Runtime             string   `json:"runtime,omitempty"`
	LogsURL             string   `json:"logs_url,omitempty"`
	Bypass              bool     `json:"bypass,omitempty"`
	ProviderMode        string   `json:"provider_mode,omitempty"`
	RegisteredProviders []string `json:"registered_providers,omitempty"`
	ProviderWarnings    []string `json:"provider_warnings,omitempty"`
}

type sessionResponse struct {
	Name                string                       `json:"name"`
	DisplayName         string                       `json:"display_name,omitempty"`
	Color               string                       `json:"color"`
	Branch              string                       `json:"branch"`
	ProjectAlias        string                       `json:"project_alias,omitempty"`
	Ports               map[string]int               `json:"ports"`
	Routes              map[string]string            `json:"routes"`
	ExternalRoutes      map[string]string            `json:"external_routes,omitempty"`
	TargetType          string                       `json:"target_type"`
	AttentionFlag       bool                         `json:"attention_flag"`
	ArtifactCount       int                          `json:"artifact_count"`
	FocusedArtifactID   string                       `json:"focused_artifact_id,omitempty"`
	UnseenArtifactCount int                          `json:"unseen_artifact_count,omitempty"`
	Gatepost            *gatepostResponse            `json:"gatepost,omitempty"`
	Stale               session.StaleStatus          `json:"stale"`
	Status              session.SessionStatusSummary `json:"status"`
}

func buildSessionResponse(sess *session.Session) sessionResponse {
	externalDomain := viper.GetString("external_domain")
	externalRoutes := make(map[string]string)
	if externalDomain != "" {
		// Prefer tunnel URLs for every service the UI can display. Some older
		// sessions have stored Caddy routes whose service labels are not present
		// in Ports (or vice versa), so derive external hostnames from both sets.
		for svc := range sess.Routes {
			h := caddy.BuildExternalHostname(sess.Name, svc, sess.ProjectAlias, externalDomain)
			if h != "" {
				externalRoutes[svc] = h
			}
		}
		for svc := range sess.Ports {
			h := caddy.BuildExternalHostname(sess.Name, svc, sess.ProjectAlias, externalDomain)
			if h != "" {
				externalRoutes[svc] = h
			}
		}
	}
	artifactCount, focusedArtifactID, unseenArtifactCount := artifactCountAndFocus(sess)
	var gatepost *gatepostResponse
	if sess.Target.Gatepost.Enabled {
		logsURL := ""
		if sess.Target.Gatepost.LogsURL != "" {
			// Point at the same-origin reverse proxy so the Logs UI works through
			// Caddy / the Cloudflare tunnel and on other devices.
			logsURL = gatepostLogsProxyURL(sess.Name)
		}
		gatepost = &gatepostResponse{Enabled: true, Runtime: sess.Target.Gatepost.Runtime, LogsURL: logsURL, Bypass: sess.Target.Gatepost.Bypass, ProviderMode: sess.Target.Gatepost.ProviderMode, RegisteredProviders: sess.Target.Gatepost.RegisteredProviders, ProviderWarnings: sess.Target.Gatepost.ProviderWarnings}
	}
	return sessionResponse{
		Name:                sess.Name,
		DisplayName:         sess.DisplayName,
		Color:               sess.EffectiveColor(),
		Branch:              sess.Branch,
		ProjectAlias:        sess.ProjectAlias,
		Ports:               sess.Ports,
		Routes:              sess.Routes,
		ExternalRoutes:      externalRoutes,
		TargetType:          sess.TargetType(),
		AttentionFlag:       sess.AttentionFlag,
		ArtifactCount:       artifactCount,
		FocusedArtifactID:   focusedArtifactID,
		UnseenArtifactCount: unseenArtifactCount,
		Gatepost:            gatepost,
	}
}

func handleGatepostLogsRedirect(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("session")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session query param required"})
		return
	}
	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sess, ok := store.GetSession(name)
	if !ok || !sess.Target.Gatepost.Enabled || sess.Target.Gatepost.LogsURL == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "gatepost logs not found"})
		return
	}
	// Redirect to the same-origin reverse proxy. This keeps the Logs UI
	// reachable through Caddy / the Cloudflare tunnel (and on other devices)
	// without publishing the per-session logs port or leaking the access token
	// into the browser URL — the proxy injects the token server-side.
	http.Redirect(w, r, gatepostLogsProxyURL(sess.Name), http.StatusFound)
}

func handleListSessions(w http.ResponseWriter, r *http.Request) {
	cacheKey := os.Getenv("HOME") + "|" + session.SessionsMetadataFingerprint() + "|" + strconv.Itoa(defaultStaleDays())
	if payload, ok := getCachedSessionList(cacheKey); ok {
		writeJSON(w, http.StatusOK, payload)
		return
	}

	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	threshold, err := webStaleThreshold(defaultStaleDays())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	summary := session.AnalyzeStaleSessionsWithOptions(store, session.FastStaleAnalysisOptions(threshold))
	staleByName := make(map[string]session.StaleStatus, len(summary.Statuses))
	for _, status := range summary.Statuses {
		staleByName[status.SessionName] = status
	}

	sessions := make([]sessionResponse, 0, len(store.Sessions))
	for _, sess := range store.Sessions {
		resp := buildSessionResponse(sess)
		resp.Stale = staleByName[sess.Name]
		resp.Status = session.DeriveSessionStatus(sess, resp.Stale, resp.ArtifactCount, resp.UnseenArtifactCount)
		sessions = append(sessions, resp)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	payload := map[string]any{"sessions": sessions, "stale_summary": summary}
	setCachedSessionList(cacheKey, payload)
	writeJSON(w, http.StatusOK, payload)
}

func handleStaleSessions(w http.ResponseWriter, r *http.Request) {
	days, err := parseStaleDays(r.URL.Query().Get("days"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	threshold, err := webStaleThreshold(days)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	cacheKey := os.Getenv("HOME") + "|" + session.SessionsMetadataFingerprint() + "|" + strconv.Itoa(days)
	if payload, ok := getCachedFullStaleScan(cacheKey); ok {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	payload, err := computeCachedFullStaleScan(cacheKey, func() (map[string]any, error) {
		store, err := session.LoadSessions()
		if err != nil {
			return nil, err
		}
		summary := session.AnalyzeStaleSessionsWithOptions(store, session.CleanupStaleAnalysisOptions(threshold))
		// Surface any previously-persisted review so the panel can show prior
		// results, but do not auto-run reviews here: reviews are explicit,
		// user-triggered actions in the panel.
		for i := range summary.Statuses {
			status := &summary.Statuses[i]
			if sess, ok := store.GetSession(status.SessionName); ok && sess.Review != nil {
				status.CleanupReview = sess.Review
			}
		}
		return map[string]any{"stale_summary": summary}, nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

type createSessionRequest struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	Target  string `json:"target"`
}

func isValidSessionTarget(target string) bool {
	switch target {
	case "", "host", "docker", "gatepost":
		return true
	default:
		return false
	}
}

// sessionCreateJobs tracks in-progress async session creations.
var sessionCreateJobs sync.Map // name -> *sessionCreateJob

type sessionCreateJob struct {
	Name      string
	Done      chan struct{}
	Err       error
	mu        sync.Mutex
	Messages  []string // progress lines from stdout
	StartedAt time.Time
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
	if !isValidSessionTarget(req.Target) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session target"})
		return
	}

	// Start creation asynchronously so long-running targets (e.g. Gatepost
	// Docker) don't time out the HTTP connection.
	job := &sessionCreateJob{Name: req.Name, Done: make(chan struct{}), StartedAt: time.Now()}
	if existing, loaded := sessionCreateJobs.LoadOrStore(req.Name, job); loaded {
		// Allow retry if the previous job is stale (>6 min, covers 5 min timeout + 60s buffer).
		old := existing.(*sessionCreateJob)
		if time.Since(old.StartedAt) < 6*time.Minute {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "session creation already in progress"})
			return
		}
		// Stale job — replace it.
		sessionCreateJobs.Store(req.Name, job)
	}
	args := []string{"session", "create", "--no-tmux"}
	if req.Project != "" {
		args = append(args, "--project", req.Project)
	}
	if req.Target != "" {
		args = append(args, "--target", req.Target)
	}
	args = append(args, "--", req.Name)
	go func() {
		defer close(job.Done)
		job.Err = runSelfWithProgress(args, func(line string) {
			job.mu.Lock()
			if len(job.Messages) < 50 { // cap to avoid unbounded growth
				job.Messages = append(job.Messages, line)
			}
			job.mu.Unlock()
		})
		// Keep job in map for 60s after completion so the status endpoint can
		// return the error to the browser even if it polls slightly late.
		time.AfterFunc(60*time.Second, func() { sessionCreateJobs.Delete(req.Name) })
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"name": req.Name, "status": "creating"})
}

func handleSessionCreateStatus(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	v, inProgress := sessionCreateJobs.Load(name)
	if inProgress {
		job := v.(*sessionCreateJob)
		select {
		case <-job.Done:
			// just finished — fall through to session lookup below
		default:
			job.mu.Lock()
			msgs := append([]string(nil), job.Messages...)
			job.mu.Unlock()
			writeJSON(w, http.StatusAccepted, map[string]interface{}{"name": name, "status": "creating", "messages": msgs})
			return
		}
	}
	store, err := session.LoadSessions()
	if err != nil || store == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if sess, ok := store.Sessions[name]; ok {
		invalidateSessionListCache()
		writeJSON(w, http.StatusOK, buildSessionResponse(sess))
	} else {
		// Check if job errored
		if inProgress {
			job := v.(*sessionCreateJob)
			if job.Err != nil {
				// Strip leading "exit status N: " prefix for cleaner UI messages.
				msg := job.Err.Error()
				if i := strings.Index(msg, ": "); i >= 0 && strings.HasPrefix(msg, "exit status") {
					msg = msg[i+2:]
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
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
	invalidateSessionListCache()
	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteStaleCleanSessions(w http.ResponseWriter, r *http.Request) {
	days, err := parseStaleDays(r.URL.Query().Get("days"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := runSelf("session", "prune", "--force", "--days", strconv.Itoa(days)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateSessionListCache()
	w.WriteHeader(http.StatusNoContent)
}

func handleRenameSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	displayName := r.URL.Query().Get("display_name")
	if displayName != "" && !session.IsValidDisplayName(displayName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid display name"})
		return
	}
	args := []string{"session", "rename"}
	if displayName == "" {
		args = append(args, "--clear", "--", name)
	} else {
		args = append(args, "--", name, displayName)
	}
	if err := runSelf(args...); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateSessionListCache()
	w.WriteHeader(http.StatusNoContent)
}

type reviewSessionRequest struct {
	Base string `json:"base"`
}

func handleGetSessionReview(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	if review, err := session.LoadSessionReviewDetails(name); err == nil {
		writeJSON(w, http.StatusOK, review)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load review details: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "review not found"})
}

func handleReviewSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	var req reviewSessionRequest
	if r.Body != nil && r.Body != http.NoBody {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
	}

	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sess, ok := store.GetSession(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	review, err := session.ReviewSession(sess, session.ReviewOptions{BaseBranch: req.Base})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Persist so the result survives reloads and appears in the next stale scan.
	if err := session.PersistSessionReview(name, review); err != nil {
		// Non-fatal: still return the review the caller just computed.
		fmt.Fprintf(os.Stderr, "warning: failed to persist review for %s: %v\n", name, err)
	}
	invalidateSessionListCache()
	writeJSON(w, http.StatusOK, review)
}

func handleMarkSessionReviewed(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	if err := markSessionReviewed(name, time.Now()); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateSessionListCache()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reviewed"})
}

func handleColorSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	color := r.URL.Query().Get("color")
	if name == "" || color == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and color query params required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	if !session.IsValidColor(color) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid color"})
		return
	}
	if err := runSelf("session", "color", "--", name, color); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateSessionListCache()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFlagSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	reason := r.URL.Query().Get("reason")
	if reason == "" {
		reason = "manual"
	}
	if err := session.SetAttentionFlag(name, reason); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateSessionListCache()
	payload, _ := json.Marshal(map[string]any{
		"session": name,
		"flagged": true,
		"reason":  reason,
	})
	s.hub.broadcastEvent("flag", string(payload))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnflagSession(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	if err := session.ClearAttentionFlag(name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	invalidateSessionListCache()
	payload, _ := json.Marshal(map[string]any{
		"session": name,
		"flagged": false,
	})
	s.hub.broadcastEvent("flag", string(payload))
	w.WriteHeader(http.StatusNoContent)
}

// handleFlagNotify broadcasts a flag SSE event without touching metadata.
// Used by the CLI after it has already written metadata via session.SetAttentionFlag.
func (s *Server) handleFlagNotify(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}
	flaggedStr := r.URL.Query().Get("flagged")
	flagged := flaggedStr != "false"
	reason := r.URL.Query().Get("reason")
	if reason == "" && flagged {
		reason = "manual"
	}
	invalidateSessionListCache()
	payload, _ := json.Marshal(map[string]any{
		"session": name,
		"flagged": flagged,
		"reason":  reason,
	})
	s.hub.broadcastEvent("flag", string(payload))
	w.WriteHeader(http.StatusNoContent)
}

// resolveWebSession returns "<name>-web" if the web-grouped session exists,
// otherwise returns name. All web-facing tmux operations should target the
// web session so that active-window state and client sizes are correct for
// the browser viewport, not a terminal client that may be attached to the
// base session.
func resolveWebSession(name string) string {
	if execTmuxRun("has-session", "-t", "="+name+"-web") == nil {
		return name + "-web"
	}
	return name
}

func exactTmuxSessionTarget(name string) string {
	return "=" + resolveWebSession(name)
}

func exactTmuxWindowTarget(name, window string) string {
	return exactTmuxSessionTarget(name) + ":" + window
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

type activePaneResponse struct {
	Pane int `json:"pane"`
}

type paneContentResponse struct {
	Content string `json:"content"`
	Target  string `json:"target"`
}

const paneCaptureLines = session.DefaultTmuxHistoryLimit

var errInvalidPane = errors.New("pane must be a numeric index")
var ansiEscapePattern = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-_])`)

func stripANSI(s string) string {
	return ansiEscapePattern.ReplaceAllString(s, "")
}

func paneCaptureTarget(name, pane string) (string, error) {
	target := exactTmuxSessionTarget(name)
	if pane == "" {
		return target + ":", nil
	}
	if _, err := strconv.Atoi(pane); err != nil {
		return "", errInvalidPane
	}
	return target + ":." + pane, nil
}

func capturePaneContent(name, pane string) (paneContentResponse, error) {
	target, err := paneCaptureTarget(name, pane)
	if err != nil {
		return paneContentResponse{}, err
	}

	out, err := execTmuxOutput("capture-pane", "-t", target, "-p", "-S", fmt.Sprintf("-%d", paneCaptureLines))
	if err != nil {
		return paneContentResponse{}, err
	}

	return paneContentResponse{
		Content: strings.TrimRight(stripANSI(string(out)), "\n"),
		Target:  target,
	}, nil
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

	// Use tab as delimiter so window names that contain spaces are preserved.
	out, err := execTmuxOutput("list-windows", "-t", exactTmuxSessionTarget(name), "-F",
		"#{window_index}\t#{window_name}\t#{window_active}")
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
		parts := strings.SplitN(line, "\t", 3)
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

func handleActivePane(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}

	out, err := execTmuxOutput("display-message", "-t", exactTmuxSessionTarget(name)+":", "-p", "#{pane_index}")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve active pane"})
		return
	}
	pane, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve active pane"})
		return
	}
	writeJSON(w, http.StatusOK, activePaneResponse{Pane: pane})
}

// handlePaneContent captures recent scrollback from the active pane in the
// web-facing tmux session. Optional ?pane=<index> selects a specific pane in
// the active window; otherwise tmux uses the active pane for the target.
func handlePaneContent(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name query param required"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}

	resp, status, err := loadPaneContentResponse(name, r.URL.Query().Get("pane"))
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func loadPaneContentResponse(name, pane string) (paneContentResponse, int, error) {
	resp, err := capturePaneContent(name, pane)
	if err == nil {
		return resp, http.StatusOK, nil
	}
	if errors.Is(err, errInvalidPane) {
		return paneContentResponse{}, http.StatusBadRequest, err
	}
	return paneContentResponse{}, http.StatusInternalServerError, fmt.Errorf("failed to capture pane content")
}

func handlePaneContentText(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query param required", http.StatusBadRequest)
		return
	}
	if !session.IsValidSessionName(name) {
		http.Error(w, "invalid session name", http.StatusBadRequest)
		return
	}

	resp, status, err := loadPaneContentResponse(name, r.URL.Query().Get("pane"))
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, resp.Content)
}

func handlePaneContentView(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query param required", http.StatusBadRequest)
		return
	}
	if !session.IsValidSessionName(name) {
		http.Error(w, "invalid session name", http.StatusBadRequest)
		return
	}

	resp, status, err := loadPaneContentResponse(name, r.URL.Query().Get("pane"))
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paneContentViewTmpl.Execute(w, struct {
		Name    string
		Target  string
		Content string
	}{
		Name:    name,
		Target:  resp.Target,
		Content: resp.Content,
	}); err != nil {
		http.Error(w, "failed to render pane content", http.StatusInternalServerError)
	}
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
	if err := execTmuxRun("select-window", "-t", exactTmuxWindowTarget(name, window)); err != nil {
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
	target := exactTmuxSessionTarget(name)
	_ = execTmuxRun("refresh-client", "-t", target)
	resizeWindowToClient(target)
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
	out, err := execTmuxOutput("list-clients", "-t", target, "-F", "#{client_activity} #{client_width} #{client_height}")
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
	_ = execTmuxRun("resize-window", "-t", target,
		"-x", strconv.Itoa(latestW),
		"-y", strconv.Itoa(latestH))
}

// handleSendKeys runs `tmux send-keys -t session: key`, delivering the key to the
// session's current window/pane regardless of which tmux client is active.
//
// mode=literal (query param): send the entire keys string verbatim using
// tmux's -l flag. Used for injecting file paths (which may contain spaces).
//
// mode=keys (default): split on whitespace, validate each token, and send as
// named tmux key sequences (e.g. "C-b Enter Escape").
func handleSendKeys(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	keys := r.URL.Query().Get("keys")
	if name == "" || keys == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and keys required"})
		return
	}
	// Guard against oversized payloads before any further processing.
	if len(keys) > 4096 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "keys parameter too long"})
		return
	}
	if !requireValidSession(w, name) {
		return
	}

	// send-keys needs a pane-capable target. A bare exact session target like
	// "=name-web" fails with "can't find pane" on tmux; append ':' to target
	// the active pane in the active window for the resolved web session.
	target := exactTmuxSessionTarget(name) + ":"
	mode := r.URL.Query().Get("mode")

	if mode == "literal" {
		// Send the text verbatim — used for inserting file paths into the active pane.
		// -l disables special-key interpretation so spaces are not split by tmux.
		if err := execTmuxRun("send-keys", "-t", target, "-l", "--", keys); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Default mode: split on whitespace so callers can send multiple named keystrokes
	// (e.g. "C-b C-b"). Cap at 256 chars for named-key payloads.
	if len(keys) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "keys parameter too long"})
		return
	}
	keyList := strings.Fields(keys)
	if len(keyList) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key sequence"})
		return
	}
	// Reject tokens starting with "-" to prevent them from being parsed as
	// tmux flags rather than key names.
	for _, key := range keyList {
		if strings.HasPrefix(key, "-") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key sequence"})
			return
		}
	}
	// Use resolveWebSession so keys land in the correct pane when the browser
	// and a terminal client are on different windows.
	// Use -- to ensure no key token is misinterpreted as a tmux send-keys flag.
	args := append([]string{"send-keys", "-t", target, "--"}, keyList...)
	if err := execTmuxRun(args...); err != nil {
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
// ~/.devx/uploads/<session>/{hex}.ext, and returns the path as JSON.
// For Gatepost sessions the returned path uses the container mount point
// so the agent can access the file directly.
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

	sessionName := r.FormValue("session")

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

	// Per-session upload directory so each session is isolated and Gatepost
	// containers only see their own files.
	uploadSubdir := "uploads"
	if sessionName != "" {
		uploadSubdir = filepath.Join("uploads", sessionName)
	}
	uploadDir := filepath.Join(home, ".devx", uploadSubdir)
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

	// For Gatepost sessions, return the container-side path so the agent
	// can read the file from the mounted uploads directory.
	resultPath := destPath
	if sessionName != "" {
		store, _ := session.LoadSessions()
		if store != nil {
			if sess, ok := store.Sessions[sessionName]; ok && sess.Target.Gatepost.Enabled {
				resultPath = filepath.Join("/root/.devx/uploads", filename)
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"path": resultPath})
}

// handleServeUpload serves files from ~/.devx/uploads/ by filename.
// Supports both legacy flat paths (/uploads/{file}) and per-session
// paths (/uploads/{session}/{file}). Path traversal is prevented by
// rejecting ".." segments.
func handleServeUpload(w http.ResponseWriter, r *http.Request) {
	subpath := strings.TrimPrefix(r.URL.Path, "/uploads/")
	if subpath == "" || strings.Contains(subpath, "..") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Allow at most one slash (session/filename).
	parts := strings.SplitN(subpath, "/", 3)
	if len(parts) > 2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		http.Error(w, "cannot find home dir", http.StatusInternalServerError)
		return
	}
	filePath := filepath.Join(home, ".devx", "uploads", subpath)

	// Serve the file — http.ServeFile sets Content-Type, ETag, etc.
	http.ServeFile(w, r, filePath)
}

// handleShow accepts a multipart image upload from the CLI, saves it to
// ~/.devx/uploads/, then broadcasts its URL to all connected SSE clients.
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	// Cap the raw request body to 20 MB before parsing.
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to parse form"})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image field required"})
		return
	}
	defer file.Close()

	// Sniff magic bytes to determine MIME type — never trust client Content-Type.
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	ct := http.DetectContentType(buf[:n])
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

	uploadURL := "/uploads/" + filename
	// Broadcast to all connected browser clients.
	payload, _ := json.Marshal(map[string]string{
		"url":  uploadURL,
		"name": header.Filename,
	})
	s.hub.broadcast(string(payload))

	writeJSON(w, http.StatusOK, map[string]string{"path": destPath, "url": uploadURL})
}

// runSelf re-invokes the devx binary with the given args.
// This reuses all existing CLI logic without duplicating it.
// TMUX and TMUX_PANE are stripped so that commands like "session create"
// don't detect they're inside tmux and skip launching the session.
func runSelf(args ...string) error {
	return runSelfWithProgress(args, nil)
}

func runSelfWithProgress(args []string, onLine func(string)) error {
	return runSelfTimeoutProgress(5*time.Minute, args, onLine)
}

func runSelfTimeoutProgress(timeout time.Duration, args []string, onLine func(string)) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}
	self = runSelfExecutable(self)
	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "TMUX=") && !strings.HasPrefix(e, "TMUX_PANE=") {
			env = append(env, e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Env = env
	cmd.Stderr = &stderr
	if onLine != nil {
		pr, pw, err := os.Pipe()
		if err == nil {
			cmd.Stdout = pw
			go func() {
				defer pr.Close()
				buf := make([]byte, 4096)
				var line []byte
				for {
					n, err := pr.Read(buf)
					for _, b := range buf[:n] {
						if b == '\n' {
							if s := strings.TrimSpace(string(line)); s != "" {
								onLine(s)
							}
							line = line[:0]
						} else {
							line = append(line, b)
						}
					}
					if err != nil {
						break
					}
				}
			}()
			defer pw.Close()
		}
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("timed out after %s: %w", timeout, ctx.Err())
		}
		if stderr.Len() > 0 {
			// Take only the first line — cobra appends full usage text after the error.
			firstLine := bytes.TrimSpace(bytes.SplitN(stderr.Bytes(), []byte{'\n'}, 2)[0])
			return fmt.Errorf("%s", firstLine)
		}
		return err
	}
	return nil
}

func runSelfExecutable(current string) string {
	if override := os.Getenv("DEVX_CLI_BINARY"); override != "" {
		return override
	}
	if strings.Contains(filepath.Base(current), "devx-desktop") {
		// The desktop shell runs the same web package, but re-executing itself for
		// CLI subcommands would open a second desktop window instead of creating a
		// session. Prefer the real devx CLI from PATH when embedded in desktop.
		if cli, err := exec.LookPath("devx"); err == nil {
			return cli
		}
	}
	return current
}
