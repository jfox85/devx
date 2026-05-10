package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jfox85/devx/session"
)

func testSession(t *testing.T) *session.Session {
	t.Helper()
	dir := t.TempDir()
	return &session.Session{Name: "feature/test", Path: dir}
}

func TestLoadManifestMissingReturnsEmpty(t *testing.T) {
	sess := testSession(t)
	m, err := LoadManifest(sess)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Version != ManifestVersion || m.Session != sess.Name || len(m.Artifacts) != 0 {
		t.Fatalf("unexpected manifest: %#v", m)
	}
}

func TestSaveAndLoadManifest(t *testing.T) {
	sess := testSession(t)
	now := time.Date(2026, 4, 25, 1, 2, 3, 0, time.UTC)
	m := NewManifest(sess.Name)
	m.Artifacts = append(m.Artifacts, Artifact{ID: "plan-test-20260425010203", Type: "plan", Title: "Plan", File: "plan.html", Created: now, Retention: DefaultRetention})
	if err := SaveManifest(sess, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	loaded, err := LoadManifest(sess)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(loaded.Artifacts) != 1 || loaded.Artifacts[0].ID != m.Artifacts[0].ID {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}
}

func TestLoadManifestRejectsMalformedJSON(t *testing.T) {
	sess := testSession(t)
	if err := os.MkdirAll(DirForSession(sess), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ManifestPath(sess), []byte(`{bad`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(sess); err == nil {
		t.Fatal("expected malformed manifest error")
	}
}

func TestValidateRelativePathRejectsTraversal(t *testing.T) {
	bad := []string{"", "../secret", "a/../../secret", "/tmp/file", ".."}
	for _, p := range bad {
		if err := ValidateRelativePath(p); err == nil {
			t.Fatalf("expected %q to be rejected", p)
		}
	}
	if err := ValidateRelativePath("screenshots/login.png"); err != nil {
		t.Fatalf("expected safe path: %v", err)
	}
}

func TestValidateFolderPathRejectsUnsafeFolders(t *testing.T) {
	bad := []string{"", "../secret", "a/../secret", "/tmp/file", "..", "workflow//run", "workflow/", "./workflow", "workflow/./run", `C:\tmp\file`}
	for _, p := range bad {
		if _, err := NormalizeFolderPath(p); err == nil {
			t.Fatalf("expected %q to be rejected", p)
		}
	}
	got, err := NormalizeFolderPath(`workflow\run-1\qa`)
	if err != nil {
		t.Fatalf("expected safe folder: %v", err)
	}
	if got != "workflow/run-1/qa" {
		t.Fatalf("normalized folder = %q", got)
	}
}

func TestLoadManifestMissingFolderIsBackwardCompatible(t *testing.T) {
	sess := testSession(t)
	if err := os.MkdirAll(DirForSession(sess), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "version": 1,
  "session": "feature/test",
  "artifacts": [
    {
      "id": "doc-old",
      "type": "document",
      "title": "Old Doc",
      "file": "old.md",
      "created": "2026-04-25T10:30:00Z"
    }
  ]
}`)
	if err := os.WriteFile(ManifestPath(sess), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(sess)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Folder != "" || m.Artifacts[0].File != "old.md" {
		t.Fatalf("unexpected manifest: %#v", m.Artifacts)
	}
}

func TestSafeJoinStaysInsideBase(t *testing.T) {
	base := t.TempDir()
	joined, err := SafeJoin(base, "logs/test.log")
	if err != nil {
		t.Fatalf("SafeJoin: %v", err)
	}
	want := filepath.Join(base, "logs", "test.log")
	if joined != want {
		t.Fatalf("got %q want %q", joined, want)
	}
	if _, err := SafeJoin(base, "../outside"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestDetectTypeAndGenerateID(t *testing.T) {
	if got := DetectType("shot.PNG"); got != "screenshot" {
		t.Fatalf("DetectType png = %q", got)
	}
	if got := DetectType("run.log"); got != "log" {
		t.Fatalf("DetectType log = %q", got)
	}
	now := time.Date(2026, 4, 25, 10, 30, 0, 0, time.UTC)
	if got := GenerateID("plan", "Auth implementation plan!", now); got != "plan-auth-implementation-plan-20260425103000-000000000" {
		t.Fatalf("GenerateID = %q", got)
	}
}

func TestParseTagsDedupesCaseInsensitive(t *testing.T) {
	tags := ParseTags("Design, design, QA, qa")
	want := []string{"Design", "QA"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("ParseTags = %#v want %#v", tags, want)
	}
}

func TestAddCreatesManifestFileAndTheme(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("<h1>Plan</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 4, 25, 10, 30, 0, 0, time.UTC)
	a, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Auth Plan", Retention: ArchiveRetention, Agent: "test", Now: created})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if a.ID != "plan-auth-plan-20260425103000-000000000" || a.File != "plan.html" {
		t.Fatalf("unexpected artifact: %#v", a)
	}
	if _, err := os.Stat(filepath.Join(DirForSession(sess), "plan.html")); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(DirForSession(sess), "theme.css")); err != nil {
		t.Fatalf("theme missing: %v", err)
	}
	m, err := LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 {
		t.Fatalf("expected one artifact, got %d", len(m.Artifacts))
	}
}

func TestAddCreatesArtifactInNestedFolder(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(source, []byte("# Plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Add(sess, AddOptions{Source: source, Type: "document", Title: "Plan", Folder: "workflow/run-123", Destination: "10-plan.md"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if a.Folder != "workflow/run-123" || a.File != "workflow/run-123/10-plan.md" {
		t.Fatalf("unexpected artifact paths: %#v", a)
	}
	if _, err := os.Stat(filepath.Join(DirForSession(sess), "workflow", "run-123", "10-plan.md")); err != nil {
		t.Fatalf("nested artifact file missing: %v", err)
	}
	m, err := LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Folder != "workflow/run-123" || m.Artifacts[0].File != "workflow/run-123/10-plan.md" {
		t.Fatalf("manifest did not persist folder: %#v", m.Artifacts)
	}
}

func TestAddNestedFolderAvoidsCollisions(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(source, []byte("# Plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Add(sess, AddOptions{Source: source, Type: "document", Title: "Plan A", Folder: "workflow/run", Destination: "plan.md"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Add(sess, AddOptions{Source: source, Type: "document", Title: "Plan B", Folder: "workflow/run", Destination: "plan.md"})
	if err != nil {
		t.Fatal(err)
	}
	if first.File != "workflow/run/plan.md" || second.File != "workflow/run/plan-2.md" {
		t.Fatalf("unexpected collision paths: %q %q", first.File, second.File)
	}
}

func TestSecureExistingPathRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := SecureExistingPath(base, "link.txt"); err == nil {
		t.Fatal("expected symlink to be rejected")
	}
}

func TestAddRejectsSymlinkArtifactRoot(t *testing.T) {
	sess := testSession(t)
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, DirForSession(sess)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Plan"}); err == nil {
		t.Fatal("expected symlink artifact root error")
	}
}

func TestAddRejectsSymlinkDestination(t *testing.T) {
	sess := testSession(t)
	if err := os.MkdirAll(DirForSession(sess), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(DirForSession(sess), "plan.html")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Plan", Destination: "plan.html", ID: "plan"}); err == nil {
		t.Fatal("expected symlink destination error")
	}
}

func TestAddRejectsReservedDestination(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: source, Type: "document", Title: "Manifest", Destination: ManifestName}); err == nil {
		t.Fatal("expected reserved destination error")
	}
}

func TestFailedAddRemovesStrayDestination(t *testing.T) {
	sess := testSession(t)
	missing := filepath.Join(t.TempDir(), "missing.txt")
	if _, err := Add(sess, AddOptions{Source: missing, Type: "document", Title: "Missing", Destination: "missing.txt"}); err == nil {
		t.Fatal("expected add failure")
	}
	if _, err := os.Lstat(filepath.Join(DirForSession(sess), "missing.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected stray destination removed, got %v", err)
	}
}

func TestAddRejectsUnsafeDestination(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Plan", Destination: "../plan.html"})
	if err == nil {
		t.Fatal("expected unsafe destination error")
	}
}

func TestAddRejectsUnsafeFolder(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Plan", Folder: "workflow/../secret", Destination: "plan.html"})
	if err == nil {
		t.Fatal("expected unsafe folder error")
	}
	if _, err := os.Lstat(filepath.Join(DirForSession(sess), "secret", "plan.html")); !os.IsNotExist(err) {
		t.Fatalf("unsafe folder wrote a file or stat failed unexpectedly: %v", err)
	}
}

func TestRemovePreservesAssetThatIsStandaloneArtifact(t *testing.T) {
	sess := testSession(t)
	assetSource := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(assetSource, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: assetSource, Type: "screenshot", Title: "Image", Destination: "assets/image.png"}); err != nil {
		t.Fatal(err)
	}
	htmlSource := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(htmlSource, []byte(`<img src="assets/image.png">`), 0o644); err != nil {
		t.Fatal(err)
	}
	page, err := Add(sess, AddOptions{Source: htmlSource, Type: "document", Title: "Page", Destination: "page.html"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(sess, page.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(DirForSession(sess), "assets", "image.png")); err != nil {
		t.Fatalf("standalone artifact primary file should remain: %v", err)
	}
}

func TestAddDiscoversNestedCSSAssetsAndRemoveDeletesUnreferencedAssets(t *testing.T) {
	sess := testSession(t)
	assetDir := filepath.Join(DirForSession(sess), "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "style.css"), []byte(`body{background:url("bg.png")}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "bg.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "page.html")
	if err := os.WriteFile(source, []byte(`<link rel="stylesheet" href="assets/style.css">`), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := Add(sess, AddOptions{Source: source, Type: "document", Title: "Page", Destination: "page.html"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"assets/style.css": false, "assets/bg.png": false}
	for _, asset := range a.Assets {
		if _, ok := want[asset]; ok {
			want[asset] = true
		}
	}
	for asset, found := range want {
		if !found {
			t.Fatalf("expected asset %s in %#v", asset, a.Assets)
		}
	}
	if _, err := Remove(sess, a.ID); err != nil {
		t.Fatal(err)
	}
	for asset := range want {
		if _, err := os.Stat(filepath.Join(DirForSession(sess), asset)); !os.IsNotExist(err) {
			t.Fatalf("expected asset %s removed, got %v", asset, err)
		}
	}
}

func TestConcurrentAddsDoNotLoseManifestUpdates(t *testing.T) {
	sess := testSession(t)
	sourceDir := t.TempDir()
	const count = 20
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			source := filepath.Join(sourceDir, fmt.Sprintf("%02d.txt", i))
			if err := os.WriteFile(source, []byte(fmt.Sprintf("file %d", i)), 0o644); err != nil {
				errCh <- err
				return
			}
			_, err := Add(sess, AddOptions{Source: source, Type: "document", Title: fmt.Sprintf("Doc %d", i), ID: fmt.Sprintf("doc-%02d", i), Destination: fmt.Sprintf("docs/%02d.txt", i)})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err := LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != count {
		t.Fatalf("expected %d artifacts, got %d", count, len(m.Artifacts))
	}
}

func TestRemoveAndSetRetention(t *testing.T) {
	sess := testSession(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 4, 25, 10, 30, 0, 0, time.UTC)
	a, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Plan", Now: created})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := SetRetention(sess, a.ID, ArchiveRetention)
	if err != nil {
		t.Fatalf("SetRetention: %v", err)
	}
	if updated.Retention != ArchiveRetention {
		t.Fatalf("retention = %q", updated.Retention)
	}
	removed, err := Remove(sess, a.ID)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed.ID != a.ID {
		t.Fatalf("removed wrong artifact: %#v", removed)
	}
	if _, err := os.Stat(filepath.Join(DirForSession(sess), a.File)); !os.IsNotExist(err) {
		t.Fatalf("artifact file still exists or stat failed unexpectedly: %v", err)
	}
	m, err := LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 0 {
		t.Fatalf("expected empty manifest, got %#v", m.Artifacts)
	}
}
