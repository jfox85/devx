package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/session"
)

func registerArtifactRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/artifacts", handleListArtifacts)
	mux.HandleFunc("GET /api/artifacts/item", handleGetArtifact)
	mux.HandleFunc("DELETE /api/artifacts/item", handleRemoveArtifact)
	mux.HandleFunc("GET /api/artifacts/file", handleServeArtifactByID)
	mux.HandleFunc("POST /api/artifacts/upload", handleUploadArtifact)
	mux.HandleFunc("POST /api/artifacts/rename", handleRenameArtifact)
	mux.HandleFunc("POST /api/artifacts/archive", handleArchiveArtifact)
	mux.HandleFunc("DELETE /api/artifacts/focus", handleClearArtifactFocus)
	mux.HandleFunc("/sessions/", handleServeSessionArtifact)
}

func artifactSessionFromRequest(w http.ResponseWriter, r *http.Request) (*session.Session, bool) {
	name := r.URL.Query().Get("session")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session query param required"})
		return nil, false
	}
	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil, false
	}
	sess, ok := store.GetSession(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return nil, false
	}
	return sess, true
}

func handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := artifactpkg.Filter(manifest.Artifacts, artifactpkg.FilterOptions{
		Type:   r.URL.Query().Get("type"),
		Tag:    r.URL.Query().Get("tag"),
		Agent:  r.URL.Query().Get("agent"),
		Search: r.URL.Query().Get("search"),
	})
	writeJSON(w, http.StatusOK, map[string]any{"session": sess.Name, "artifacts": artifactpkg.WithComputedFields(sess.Name, items)})
}

func handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id query param required"})
		return
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a, _ := artifactpkg.Find(manifest, id)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
		return
	}
	writeJSON(w, http.StatusOK, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{*a})[0])
}

func handleRemoveArtifact(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id query param required"})
		return
	}
	if _, err := artifactpkg.Remove(sess, id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleServeArtifactByID(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id query param required"})
		return
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a, _ := artifactpkg.Find(manifest, id)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
		return
	}
	serveManifestArtifactFile(w, r, sess, a.File)
}

func handleServeSessionArtifact(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/sessions/") {
		http.NotFound(w, r)
		return
	}
	rawPath := r.URL.RawPath
	if rawPath == "" {
		rawPath = r.URL.EscapedPath()
	}
	rest := strings.TrimPrefix(rawPath, "/sessions/")
	encodedSession, encodedFile, ok := strings.Cut(rest, "/artifacts/")
	if !ok || encodedSession == "" || encodedFile == "" {
		http.NotFound(w, r)
		return
	}
	name, err := url.PathUnescape(encodedSession)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := url.PathUnescape(encodedFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	store, err := session.LoadSessions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sess, ok := store.GetSession(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	serveManifestArtifactFile(w, r, sess, file)
}

func serveManifestArtifactFile(w http.ResponseWriter, r *http.Request, sess *session.Session, rel string) {
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	cleanRel := filepath.ToSlash(filepath.Clean(rel))
	allowed := cleanRel == "theme.css"
	for _, a := range manifest.Artifacts {
		if filepath.ToSlash(filepath.Clean(a.File)) == cleanRel {
			allowed = true
			break
		}
		for _, ref := range a.Assets {
			if filepath.ToSlash(filepath.Clean(ref)) == cleanRel {
				allowed = true
				break
			}
		}
		if allowed {
			break
		}
	}
	if !allowed {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	abs, err := artifactpkg.SecureExistingPath(artifactpkg.DirForSession(sess), cleanRel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'unsafe-inline'; font-src 'self';")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, abs)
}

func handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to parse form"})
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	artifactType := r.FormValue("type")
	title := r.FormValue("title")
	retention := r.FormValue("retention")
	tags := artifactpkg.ParseTags(r.FormValue("tags"))
	summary := r.FormValue("summary")

	var added []artifactpkg.ListItem
	var addedIDs []string
	rollbackAdded := func() {
		for i := len(addedIDs) - 1; i >= 0; i-- {
			_, _ = artifactpkg.Remove(sess, addedIDs[i])
		}
	}
	if text := r.FormValue("text"); text != "" {
		if title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required for text artifacts"})
			return
		}
		format := strings.TrimPrefix(strings.TrimSpace(r.FormValue("format")), ".")
		if format == "" {
			format = "md"
		}
		dest := artifactpkg.Slugify(title) + "." + format
		a, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: "-", Reader: strings.NewReader(text), Destination: dest, Type: artifactType, Title: title, Summary: summary, Agent: "human", Retention: retention, Tags: tags})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		addedIDs = append(addedIDs, a.ID)
		added = append(added, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{a})[0])
	}

	files := r.MultipartForm.File["file"]
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			rollbackAdded()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to open uploaded file"})
			return
		}
		itemTitle := title
		if itemTitle == "" || len(files) > 1 {
			itemTitle = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
			if itemTitle == "" {
				itemTitle = header.Filename
			}
		}
		itemType := artifactType
		if itemType == "" {
			itemType = artifactpkg.DetectType(header.Filename)
		}
		dest := artifactpkg.DefaultDestination(itemType, header.Filename)
		a, addErr := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: header.Filename, Reader: file, Destination: dest, Type: itemType, Title: itemTitle, Summary: summary, Agent: "human", Retention: retention, Tags: tags})
		_ = file.Close()
		if addErr != nil {
			rollbackAdded()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": addErr.Error()})
			return
		}
		addedIDs = append(addedIDs, a.ID)
		added = append(added, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{a})[0])
	}
	if len(added) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file or text is required"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"artifacts": added})
}

type renameArtifactRequest struct {
	Title     *string  `json:"title"`
	Type      *string  `json:"type"`
	Summary   *string  `json:"summary"`
	Tags      []string `json:"tags"`
	TagsSet   bool     `json:"-"`
	Retention *string  `json:"retention"`
}

func handleRenameArtifact(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id query param required"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body too large"})
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	var req renameArtifactRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	_, req.TagsSet = raw["tags"]
	updated, err := artifactpkg.UpdateMetadata(sess, id, artifactpkg.MetadataUpdate{Title: req.Title, Type: req.Type, Summary: req.Summary, Tags: req.Tags, TagsSet: req.TagsSet, Retention: req.Retention})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{updated})[0])
}

func handleArchiveArtifact(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id query param required"})
		return
	}
	updated, err := artifactpkg.SetRetention(sess, id, artifactpkg.ArchiveRetention)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{updated})[0])
}

func handleClearArtifactFocus(w http.ResponseWriter, r *http.Request) {
	sess, ok := artifactSessionFromRequest(w, r)
	if !ok {
		return
	}
	if err := artifactpkg.ClearFocus(sess); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = clearArtifactAttentionFlag(sess.Name)
	w.WriteHeader(http.StatusNoContent)
}

func clearArtifactAttentionFlag(name string) error {
	store, err := session.LoadSessions()
	if err != nil {
		return err
	}
	return store.UpdateSession(name, func(s *session.Session) {
		if s.AttentionSource == "artifact" {
			s.AttentionFlag = false
			s.AttentionReason = ""
			s.AttentionSource = ""
			s.AttentionTime = time.Time{}
		}
	})
}

func artifactCountAndFocus(sess *session.Session) (int, string) {
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		return 0, ""
	}
	focused := ""
	for _, a := range manifest.Artifacts {
		if a.Focus {
			focused = a.ID
			break
		}
	}
	return len(manifest.Artifacts), focused
}
