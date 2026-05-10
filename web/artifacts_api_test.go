package web

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/session"
)

func setupArtifactAPITest(t *testing.T) *session.Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "devx"), 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "feature/web-artifacts", Branch: "feature/web-artifacts", Path: worktree, Ports: map[string]int{"ui": 3000}}
	store := &session.SessionStore{Sessions: map[string]*session.Session{sess.Name: sess}, NumberedSlots: map[int]string{}}
	if err := store.Save(); err != nil {
		t.Fatalf("Save sessions: %v", err)
	}
	return sess
}

func artifactMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerAPIRoutes(mux)
	registerArtifactRoutes(mux)
	return mux
}

func TestArtifactUploadListAndServe(t *testing.T) {
	sess := setupArtifactAPITest(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "Login Screenshot")
	_ = mw.WriteField("tags", "ui, login")
	fw, err := mw.CreateFormFile("file", "login.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("fake image bytes")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/artifacts/upload?session="+url.QueryEscape(sess.Name), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var uploadResp struct {
		Artifacts []artifactpkg.ListItem `json:"artifacts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &uploadResp); err != nil {
		t.Fatalf("invalid upload JSON: %v", err)
	}
	if len(uploadResp.Artifacts) != 1 {
		t.Fatalf("expected one artifact: %#v", uploadResp)
	}
	if uploadResp.Artifacts[0].Agent != "human" || uploadResp.Artifacts[0].Type != "screenshot" {
		t.Fatalf("unexpected artifact: %#v", uploadResp.Artifacts[0])
	}

	listReq := httptest.NewRequest("GET", "/api/artifacts?session="+url.QueryEscape(sess.Name), nil)
	listW := httptest.NewRecorder()
	artifactMux().ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listW.Code, listW.Body.String())
	}

	serveReq := httptest.NewRequest("GET", uploadResp.Artifacts[0].URL, nil)
	serveW := httptest.NewRecorder()
	artifactMux().ServeHTTP(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("expected served artifact 200, got %d: %s", serveW.Code, serveW.Body.String())
	}
	if got := serveW.Body.String(); got != "fake image bytes" {
		t.Fatalf("served body = %q", got)
	}
	if got := serveW.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
		t.Fatalf("expected sandbox CSP, got %q", got)
	}
	if got := serveW.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
}

func TestServeArtifactAllowsReferencedAsset(t *testing.T) {
	sess := setupArtifactAPITest(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte(`<img src="screenshots/login.png">`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: source, Type: "plan", Title: "Plan"}); err != nil {
		t.Fatal(err)
	}
	assetDir := filepath.Join(artifactpkg.DirForSession(sess), "screenshots")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "login.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/sessions/"+url.PathEscape(sess.Name)+"/artifacts/screenshots/login.png", nil)
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected referenced asset 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "png" {
		t.Fatalf("asset body = %q", got)
	}
}

func TestServeNestedArtifactURL(t *testing.T) {
	sess := setupArtifactAPITest(t)
	source := filepath.Join(t.TempDir(), "proof.html")
	if err := os.WriteFile(source, []byte("<h1>Proof</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: source, Type: "report", Title: "Proof", Folder: "workflow/run-1/40-proof", Destination: "proof.html"})
	if err != nil {
		t.Fatal(err)
	}
	item := artifactpkg.WithComputedFields(sess.Name, []artifactpkg.Artifact{a})[0]
	if item.URL != "/sessions/feature%2Fweb-artifacts/artifacts/workflow/run-1/40-proof/proof.html" || item.Path != ".artifacts/workflow/run-1/40-proof/proof.html" {
		t.Fatalf("unexpected computed paths: %#v", item)
	}
	req := httptest.NewRequest("GET", item.URL, nil)
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected nested artifact 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != "<h1>Proof</h1>" {
		t.Fatalf("nested artifact body = %q", got)
	}
}

func TestServeArtifactRequiresManifestEntry(t *testing.T) {
	sess := setupArtifactAPITest(t)
	if err := os.MkdirAll(artifactpkg.DirForSession(sess), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactpkg.DirForSession(sess), "stray.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/sessions/"+url.PathEscape(sess.Name)+"/artifacts/stray.txt", nil)
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-manifest file, got %d: %s", w.Code, w.Body.String())
	}
}

func TestArtifactUploadTextAndRename(t *testing.T) {
	sess := setupArtifactAPITest(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "Reference Notes")
	_ = mw.WriteField("format", "md")
	_ = mw.WriteField("text", "# Hello")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/artifacts/upload?session="+url.QueryEscape(sess.Name), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var uploadResp struct {
		Artifacts []artifactpkg.ListItem `json:"artifacts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &uploadResp); err != nil {
		t.Fatal(err)
	}
	id := uploadResp.Artifacts[0].ID
	renameBody := strings.NewReader(`{"title":"Renamed Notes","tags":["reference"]}`)
	renameReq := httptest.NewRequest("POST", "/api/artifacts/rename?session="+url.QueryEscape(sess.Name)+"&id="+url.QueryEscape(id), renameBody)
	renameReq.Header.Set("Content-Type", "application/json")
	renameW := httptest.NewRecorder()
	artifactMux().ServeHTTP(renameW, renameReq)
	if renameW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", renameW.Code, renameW.Body.String())
	}
	var renamed artifactpkg.ListItem
	if err := json.Unmarshal(renameW.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "Renamed Notes" || len(renamed.Tags) != 1 || renamed.Tags[0] != "reference" {
		t.Fatalf("unexpected renamed artifact: %#v", renamed)
	}
}

func TestServeArtifactRejectsTraversal(t *testing.T) {
	sess := setupArtifactAPITest(t)
	escapedSession := url.PathEscape(sess.Name)
	req := httptest.NewRequest("GET", "/sessions/"+escapedSession+"/artifacts/..%2Fsecret", nil)
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		b, _ := io.ReadAll(w.Body)
		t.Fatalf("expected 404, got %d: %s", w.Code, string(b))
	}
}

func TestArchiveAndRemoveArtifact(t *testing.T) {
	sess := setupArtifactAPITest(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("<h1>Plan</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: source, Type: "plan", Title: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	archiveReq := httptest.NewRequest("POST", "/api/artifacts/archive?session="+url.QueryEscape(sess.Name)+"&id="+url.QueryEscape(a.ID), nil)
	archiveW := httptest.NewRecorder()
	artifactMux().ServeHTTP(archiveW, archiveReq)
	if archiveW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", archiveW.Code, archiveW.Body.String())
	}
	var archived artifactpkg.ListItem
	if err := json.Unmarshal(archiveW.Body.Bytes(), &archived); err != nil {
		t.Fatal(err)
	}
	if archived.Retention != artifactpkg.ArchiveRetention {
		t.Fatalf("retention = %q", archived.Retention)
	}
	removeReq := httptest.NewRequest("DELETE", "/api/artifacts/item?session="+url.QueryEscape(sess.Name)+"&id="+url.QueryEscape(a.ID), nil)
	removeW := httptest.NewRecorder()
	artifactMux().ServeHTTP(removeW, removeReq)
	if removeW.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", removeW.Code, removeW.Body.String())
	}
	m, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 0 {
		t.Fatalf("expected removed artifact, got %#v", m.Artifacts)
	}
}

func TestClearArtifactFocusPreservesManualAttention(t *testing.T) {
	sess := setupArtifactAPITest(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("<h1>Plan</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: source, Type: "plan", Title: "Plan", Focus: true}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetAttentionFlag(sess.Name, "manual"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("DELETE", "/api/artifacts/focus?session="+url.QueryEscape(sess.Name), nil)
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := store.GetSession(sess.Name)
	if !ok || !updated.AttentionFlag || updated.AttentionReason != "manual" || updated.AttentionSource != "manual" {
		t.Fatalf("manual attention was not preserved: %#v", updated)
	}
}

func TestClearArtifactFocus(t *testing.T) {
	sess := setupArtifactAPITest(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("<h1>Plan</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: source, Type: "plan", Title: "Plan", Focus: true}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("DELETE", "/api/artifacts/focus?session="+url.QueryEscape(sess.Name), nil)
	w := httptest.NewRecorder()
	artifactMux().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if focused := artifactpkg.FocusedID(sess); focused != "" {
		t.Fatalf("focus was not cleared: %q", focused)
	}
}

func TestSessionResponseIncludesArtifactCountAndFocus(t *testing.T) {
	sess := setupArtifactAPITest(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("<h1>Plan</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := artifactpkg.Add(sess, artifactpkg.AddOptions{Source: source, Type: "plan", Title: "Plan", Focus: true})
	if err != nil {
		t.Fatal(err)
	}
	resp := buildSessionResponse(sess)
	if resp.ArtifactCount != 1 || resp.FocusedArtifactID != a.ID {
		t.Fatalf("artifact fields missing: %#v", resp)
	}
}
