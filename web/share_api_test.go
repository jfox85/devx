package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	artifactpkg "github.com/jfox85/devx/artifact"
)

func shareTestMux() *http.ServeMux {
	mux := artifactMux()
	registerShareRoutes(mux)
	return mux
}

func resetShareStoreForTest(t *testing.T) {
	t.Helper()
	shareIntentStore.Lock()
	for _, intent := range shareIntentStore.items {
		cleanupShareIntent(intent)
	}
	shareIntentStore.items = map[string]*shareIntent{}
	shareIntentStore.bytes = 0
	shareIntentStore.Unlock()
	t.Cleanup(func() {
		shareIntentStore.Lock()
		for _, intent := range shareIntentStore.items {
			cleanupShareIntent(intent)
		}
		shareIntentStore.items = map[string]*shareIntent{}
		shareIntentStore.bytes = 0
		shareIntentStore.Unlock()
	})
}

func TestShareTargetRejectsCrossOriginPost(t *testing.T) {
	resetShareStoreForTest(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("text", "hello")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/share-target", &body)
	req.Host = "127.0.0.1:7777"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	shareTestMux().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestShareTargetUnauthedStagesIntent(t *testing.T) {
	resetShareStoreForTest(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("title", "Shared Note")
	_ = mw.WriteField("text", "hello")
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/share-target", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	authMiddleware("secret", shareTestMux()).ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 without auth, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/?share=") {
		t.Fatalf("unexpected redirect %q", loc)
	}
}

func TestShareIntentValidationErrorsPreserveIntent(t *testing.T) {
	resetShareStoreForTest(t)
	if err := insertShareIntent(&shareIntent{ID: "retry", Title: "Shared", Text: "hello", Created: time.Now(), Expires: time.Now().Add(time.Minute), Bytes: 5}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	shareTestMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/share-intents/retry", strings.NewReader(`{"session":"missing"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if lookupShareIntent("retry") == nil {
		t.Fatal("expected intent preserved after validation failure")
	}
}

func TestShareIntentCommitCreatesTextArtifactAndIsOneTime(t *testing.T) {
	resetShareStoreForTest(t)
	sess := setupArtifactAPITest(t)
	intent := &shareIntent{ID: "abc", Title: "Shared", Text: "hello", Created: time.Now(), Expires: time.Now().Add(time.Minute)}
	if err := insertShareIntent(intent); err != nil {
		t.Fatal(err)
	}
	body := strings.NewReader(`{"session":"` + sess.Name + `","title":"Shared","type":"document","format":"md"}`)
	req := httptest.NewRequest("POST", "/api/share-intents/abc", body)
	w := httptest.NewRecorder()
	shareTestMux().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if lookupShareIntent("abc") != nil {
		t.Fatal("expected intent consumed")
	}
	m, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Agent != "share" {
		t.Fatalf("unexpected artifacts: %#v", m.Artifacts)
	}
	replay := httptest.NewRecorder()
	shareTestMux().ServeHTTP(replay, httptest.NewRequest("POST", "/api/share-intents/abc", strings.NewReader(`{"session":"`+sess.Name+`"}`)))
	if replay.Code != http.StatusNotFound {
		t.Fatalf("expected replay 404, got %d", replay.Code)
	}
}

func TestShareIntentCommitConcurrentSingleUse(t *testing.T) {
	resetShareStoreForTest(t)
	sess := setupArtifactAPITest(t)
	if err := insertShareIntent(&shareIntent{ID: "race", Title: "Shared", Text: "hello", Created: time.Now(), Expires: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			shareTestMux().ServeHTTP(w, httptest.NewRequest("POST", "/api/share-intents/race", strings.NewReader(`{"session":"`+sess.Name+`","title":"Shared"}`)))
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	created, notFound := 0, 0
	for code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusNotFound:
			notFound++
		default:
			t.Fatalf("unexpected code %d", code)
		}
	}
	if created != 1 || notFound != 1 {
		t.Fatalf("expected one create and one not found, got created=%d notFound=%d", created, notFound)
	}
}

func TestExpiredShareIntentIsCleanedUp(t *testing.T) {
	resetShareStoreForTest(t)
	dir, err := shareTempDir()
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(dir, "devx-share-*")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tmp.WriteString("x")
	_ = tmp.Close()
	if err := insertShareIntent(&shareIntent{ID: "expired", Files: []shareIntentFile{{Name: "x.txt", Path: tmp.Name(), Size: 1}}, Created: time.Now().Add(-time.Hour), Expires: time.Now().Add(-time.Minute), Bytes: 1}); err != nil {
		t.Fatal(err)
	}
	if got := lookupShareIntent("expired"); got != nil {
		t.Fatalf("expected expired intent removed: %#v", got)
	}
	if _, err := os.Stat(tmp.Name()); !os.IsNotExist(err) {
		t.Fatalf("expected temp file removed, got %v", err)
	}
}

func TestShareIntentAPIRequiresAuthWhenMounted(t *testing.T) {
	resetShareStoreForTest(t)
	if err := insertShareIntent(&shareIntent{ID: "auth", Text: "hello", Created: time.Now(), Expires: time.Now().Add(time.Minute), Bytes: 5}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	authMiddleware("secret", shareTestMux()).ServeHTTP(w, httptest.NewRequest("GET", "/api/share-intents/auth", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestShareTargetFileCommitCleansTempFile(t *testing.T) {
	resetShareStoreForTest(t)
	sess := setupArtifactAPITest(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("files", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("file body"))
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/share-target", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	shareTestMux().ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
	id := strings.TrimPrefix(w.Header().Get("Location"), "/?share=")
	intent := lookupShareIntent(id)
	if intent == nil || len(intent.Files) != 1 {
		t.Fatalf("missing staged file: %#v", intent)
	}
	tmpPath := intent.Files[0].Path
	commit := httptest.NewRecorder()
	shareTestMux().ServeHTTP(commit, httptest.NewRequest("POST", "/api/share-intents/"+url.PathEscape(id), strings.NewReader(`{"session":"`+sess.Name+`"}`)))
	if commit.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", commit.Code, commit.Body.String())
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp cleanup, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifactpkg.DirForSession(sess), "logs", "note.txt")); err != nil {
		t.Fatalf("expected artifact file: %v", err)
	}
	var resp struct {
		Artifacts []artifactpkg.ListItem `json:"artifacts"`
	}
	if err := json.Unmarshal(commit.Body.Bytes(), &resp); err != nil || len(resp.Artifacts) != 1 {
		t.Fatalf("bad response %#v err=%v", resp, err)
	}
}
