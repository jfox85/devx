package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestResolveFile(t *testing.T) {
	sess := &session.Session{Name: "resolve", Path: t.TempDir()}
	a, err := Add(sess, AddOptions{Source: "-", Reader: strings.NewReader("hello"), Destination: "notes.md", Type: "document", Title: "Notes"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	abs, got, err := ResolveFile(sess, a.ID)
	if err != nil {
		t.Fatalf("ResolveFile: %v", err)
	}
	if got.ID != a.ID {
		t.Fatalf("got artifact %q, want %q", got.ID, a.ID)
	}
	if data, err := os.ReadFile(abs); err != nil || string(data) != "hello" {
		t.Fatalf("resolved path %q read %q, %v", abs, data, err)
	}
	if _, _, err := ResolveFile(sess, "missing"); err == nil {
		t.Fatal("expected error for unknown id")
	}
	if _, _, err := ResolveFile(sess, ""); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestResolveFileRejectsSymlink(t *testing.T) {
	sess := &session.Session{Name: "resolve-link", Path: t.TempDir()}
	a, err := Add(sess, AddOptions{Source: "-", Reader: strings.NewReader("x"), Destination: "shot.png", Type: "screenshot", Title: "Shot"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	target := filepath.Join(DirForSession(sess), a.File)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveFile(sess, a.ID); err == nil {
		t.Fatal("expected symlinked artifact to be rejected")
	}
}

func TestSafeToOpenExternally(t *testing.T) {
	for _, f := range []string{"a.png", "dir/B.MP4", "r.html", "x.pdf", "n.md"} {
		if !SafeToOpenExternally(f) {
			t.Errorf("%s should be safe to open", f)
		}
	}
	for _, f := range []string{"run.command", "x.sh", "App.app", "a.zip", "noext", "t.jsx"} {
		if SafeToOpenExternally(f) {
			t.Errorf("%s should not be opened directly", f)
		}
	}
}
