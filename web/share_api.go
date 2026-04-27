package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/session"
)

type shareIntent struct {
	ID      string            `json:"id"`
	Title   string            `json:"title,omitempty"`
	Text    string            `json:"text,omitempty"`
	URL     string            `json:"url,omitempty"`
	Files   []shareIntentFile `json:"files,omitempty"`
	Created time.Time         `json:"created"`
	Expires time.Time         `json:"expires"`
	Bytes   int64             `json:"-"`
}

type shareIntentFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Type string `json:"type,omitempty"`
	Path string `json:"-"`
}

type shareCommitRequest struct {
	Session   string `json:"session"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	Format    string `json:"format"`
	Retention string `json:"retention"`
	Tags      string `json:"tags"`
}

const (
	shareIntentTTL        = 15 * time.Minute
	shareIntentMaxPending = 25
	shareIntentMaxBytes   = 100 << 20
)

var shareSweeperOnce sync.Once

var shareIntentStore = struct {
	sync.Mutex
	items map[string]*shareIntent
	bytes int64
}{items: map[string]*shareIntent{}}

func registerShareRoutes(mux *http.ServeMux) {
	shareSweeperOnce.Do(func() {
		cleanupStaleShareTempFiles()
		go shareIntentSweeper()
	})
	mux.HandleFunc("POST /share-target", handleShareTarget)
	mux.HandleFunc("GET /api/share-intents/", handleGetShareIntent)
	mux.HandleFunc("POST /api/share-intents/", handleCommitShareIntent)
}

func handleShareTarget(w http.ResponseWriter, r *http.Request) {
	if !allowedShareTargetOrigin(r) {
		http.Error(w, "share target origin not allowed", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "failed to parse shared content", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	id, err := randomShareID()
	if err != nil {
		http.Error(w, "failed to stage shared content", http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	intent := &shareIntent{
		ID:      id,
		Title:   strings.TrimSpace(r.FormValue("title")),
		Text:    strings.TrimSpace(r.FormValue("text")),
		URL:     strings.TrimSpace(r.FormValue("url")),
		Created: now,
		Expires: now.Add(shareIntentTTL),
	}
	intent.Bytes = int64(len(intent.Title) + len(intent.Text) + len(intent.URL))
	for _, headers := range r.MultipartForm.File {
		for _, header := range headers {
			file, err := header.Open()
			if err != nil {
				cleanupShareIntent(intent)
				http.Error(w, "failed to open shared file", http.StatusBadRequest)
				return
			}
			tmp, err := os.CreateTemp(shareTempDir(), "devx-share-*")
			if err != nil {
				_ = file.Close()
				cleanupShareIntent(intent)
				http.Error(w, "failed to store shared file", http.StatusInternalServerError)
				return
			}
			size, copyErr := io.Copy(tmp, file)
			closeErr := tmp.Close()
			_ = file.Close()
			if copyErr != nil || closeErr != nil {
				_ = os.Remove(tmp.Name())
				cleanupShareIntent(intent)
				http.Error(w, "failed to store shared file", http.StatusInternalServerError)
				return
			}
			intent.Bytes += size
			intent.Files = append(intent.Files, shareIntentFile{Name: filepath.Base(header.Filename), Size: size, Type: header.Header.Get("Content-Type"), Path: tmp.Name()})
		}
	}
	if err := insertShareIntent(intent); err != nil {
		cleanupShareIntent(intent)
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	http.Redirect(w, r, "/?share="+intent.ID, http.StatusSeeOther)
}

func handleGetShareIntent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/share-intents/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share intent not found"})
		return
	}
	intent := lookupShareIntent(id)
	if intent == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share intent not found"})
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func handleCommitShareIntent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/share-intents/")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share intent not found"})
		return
	}
	var req shareCommitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Session) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session is required"})
		return
	}
	intent := lookupShareIntent(id)
	if intent == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share intent not found"})
		return
	}
	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sess, ok := store.GetSession(req.Session)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	artifactType := req.Type
	if artifactType == "" {
		artifactType = "document"
	}
	if err := artifactpkg.ValidateType(artifactType); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	retention := req.Retention
	if retention == "" {
		retention = artifactpkg.DefaultRetention
	}
	if err := artifactpkg.ValidateRetention(retention); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	intent = takeShareIntent(id)
	if intent == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share intent not found"})
		return
	}
	defer cleanupShareIntent(intent)
	var added []artifactpkg.ListItem
	var addedIDs []string
	rollback := func() {
		for i := len(addedIDs) - 1; i >= 0; i-- {
			_, _ = artifactpkg.Remove(sess, addedIDs[i])
		}
	}
	if intent.Text != "" || intent.URL != "" {
		title := strings.TrimSpace(req.Title)
		if title == "" {
			title = firstNonEmpty(intent.Title, "Shared text")
		}
		format := strings.TrimPrefix(strings.TrimSpace(req.Format), ".")
		if format == "" {
			format = "md"
		}
		body := sharedTextBody(intent)
		a, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: "-", Reader: strings.NewReader(body), Destination: artifactpkg.Slugify(title) + "." + format, Type: artifactType, Title: title, Agent: "share", Retention: retention, Tags: artifactpkg.ParseTags(req.Tags)})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		addedIDs = append(addedIDs, a.ID)
		added = append(added, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{a})[0])
	}
	for _, f := range intent.Files {
		file, err := os.Open(f.Path)
		if err != nil {
			rollback()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shared file is no longer available"})
			return
		}
		itemType := artifactType
		if req.Type == "" {
			itemType = artifactpkg.DetectType(f.Name)
		}
		title := strings.TrimSpace(req.Title)
		if title == "" || len(intent.Files) > 1 || len(added) > 0 {
			title = strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
			if title == "" {
				title = f.Name
			}
		}
		a, addErr := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: f.Name, Reader: file, Destination: artifactpkg.DefaultDestination(itemType, f.Name), Type: itemType, Title: title, Agent: "share", Retention: retention, Tags: artifactpkg.ParseTags(req.Tags)})
		_ = file.Close()
		if addErr != nil {
			rollback()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": addErr.Error()})
			return
		}
		addedIDs = append(addedIDs, a.ID)
		added = append(added, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{a})[0])
	}
	if len(added) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "shared content is empty"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"artifacts": added})
}

func allowedShareTargetOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == "" {
		return true
	}
	return strings.EqualFold(u.Host, r.Host)
}

func insertShareIntent(intent *shareIntent) error {
	expireShareIntents(time.Now().UTC())
	shareIntentStore.Lock()
	defer shareIntentStore.Unlock()
	if len(shareIntentStore.items) >= shareIntentMaxPending || shareIntentStore.bytes+intent.Bytes > shareIntentMaxBytes {
		return fmt.Errorf("too many pending shared items; try again shortly")
	}
	shareIntentStore.items[intent.ID] = intent
	shareIntentStore.bytes += intent.Bytes
	return nil
}

func lookupShareIntent(id string) *shareIntent {
	expireShareIntents(time.Now().UTC())
	shareIntentStore.Lock()
	defer shareIntentStore.Unlock()
	return shareIntentStore.items[id]
}

func takeShareIntent(id string) *shareIntent {
	expireShareIntents(time.Now().UTC())
	shareIntentStore.Lock()
	defer shareIntentStore.Unlock()
	intent := shareIntentStore.items[id]
	if intent != nil {
		delete(shareIntentStore.items, id)
		shareIntentStore.bytes -= intent.Bytes
	}
	return intent
}

func expireShareIntents(now time.Time) {
	shareIntentStore.Lock()
	var expired []*shareIntent
	for id, intent := range shareIntentStore.items {
		if !intent.Expires.After(now) {
			expired = append(expired, intent)
			delete(shareIntentStore.items, id)
			shareIntentStore.bytes -= intent.Bytes
		}
	}
	shareIntentStore.Unlock()
	for _, intent := range expired {
		cleanupShareIntent(intent)
	}
}

func shareIntentSweeper() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		expireShareIntents(time.Now().UTC())
	}
}

func shareTempDir() string {
	dir := filepath.Join(os.TempDir(), "devx-share-target")
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			_ = os.RemoveAll(dir)
		}
	}
	_ = os.MkdirAll(dir, 0o700)
	_ = os.Chmod(dir, 0o700)
	return dir
}

func cleanupStaleShareTempFiles() {
	entries, err := os.ReadDir(shareTempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-shareIntentTTL)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "devx-share-") {
			continue
		}
		path := filepath.Join(shareTempDir(), entry.Name())
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
	}
}

func cleanupShareIntent(intent *shareIntent) {
	if intent == nil {
		return
	}
	for _, f := range intent.Files {
		_ = os.Remove(f.Path)
	}
}

func sharedTextBody(intent *shareIntent) string {
	var parts []string
	if intent.Text != "" {
		parts = append(parts, intent.Text)
	}
	if intent.URL != "" {
		parts = append(parts, intent.URL)
	}
	return strings.Join(parts, "\n\n")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func randomShareID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
