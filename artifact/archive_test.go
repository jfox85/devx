package artifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfox85/devx/session"
)

func TestArchiveSessionArtifactsUsesUniqueDirectory(t *testing.T) {
	project := t.TempDir()
	worktree := filepath.Join(project, ".worktrees", "feature-unique")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "feature-unique", Path: worktree, ProjectPath: project}
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: source, Type: "plan", Title: "Plan", Retention: ArchiveRetention}); err != nil {
		t.Fatal(err)
	}
	first, _, err := ArchiveSessionArtifacts(sess)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ArchiveSessionArtifacts(sess)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("expected unique archive dirs, both were %q", first)
	}
	for _, dir := range []string{first, second} {
		if _, err := os.Stat(filepath.Join(dir, ManifestName)); err != nil {
			t.Fatalf("expected manifest in %s: %v", dir, err)
		}
	}
}

func TestArchiveSessionArtifactsCopiesArchivedFilesAndReferencedAssets(t *testing.T) {
	project := t.TempDir()
	worktree := filepath.Join(project, ".worktrees", "feature-auth")
	if err := os.MkdirAll(filepath.Join(worktree, "input"), 0o755); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "feature-auth", Path: worktree, ProjectPath: project}
	plan := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(plan, []byte(`<link rel="stylesheet" href="./theme.css"><img src="./screenshots/login.png">`), 0o644); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(t.TempDir(), "login.png")
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "test.log")
	if err := os.WriteFile(log, []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	if _, err := Add(sess, AddOptions{Source: plan, Type: "plan", Title: "Plan", Retention: ArchiveRetention, Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: shot, Type: "screenshot", Title: "Login", Destination: "screenshots/login.png", Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(sess, AddOptions{Source: log, Type: "log", Title: "Log", Now: now}); err != nil {
		t.Fatal(err)
	}
	archiveDir, count, err := ArchiveSessionArtifacts(sess)
	if err != nil {
		t.Fatalf("ArchiveSessionArtifacts: %v", err)
	}
	if count != 1 {
		t.Fatalf("archived count = %d", count)
	}
	for _, rel := range []string{"plan.html", "screenshots/login.png", "theme.css", ManifestName} {
		if _, err := os.Stat(filepath.Join(archiveDir, rel)); err != nil {
			t.Fatalf("expected archived %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(archiveDir, "logs", "test.log")); !os.IsNotExist(err) {
		t.Fatalf("session-retained unreferenced log should not be archived: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(archiveDir, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 1 || m.Artifacts[0].Retention != ArchiveRetention {
		t.Fatalf("unexpected archive manifest: %#v", m.Artifacts)
	}
}
