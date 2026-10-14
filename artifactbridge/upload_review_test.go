//go:build darwin || linux

package artifactbridge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	artifactpkg "github.com/jfox85/devx/artifact"
)

// Review finding: registration must not follow a symlinked .artifacts. A
// directory swapped for a symlink to an outside directory, at any point
// before registration, must never cause a lock/manifest write outside.
func TestRegisterNoFollowRefusesSymlinkedArtifactsDir(t *testing.T) {
	wt := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(wt, ".artifacts")); err != nil {
		t.Fatal(err)
	}
	called := false
	err := registerNoFollow(wt, func([]byte) ([]byte, error) { called = true; return []byte("{}"), nil })
	if err == nil || called {
		t.Fatalf("registration through a symlinked .artifacts must fail before mutate: err=%v called=%v", err, called)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the worktree: %v", entries)
	}
	// Symlinked manifest.json and lock file are refused too.
	wt2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(wt2, ".artifacts"), 0o755)
	_ = os.Symlink(filepath.Join(outside, "m.json"), filepath.Join(wt2, ".artifacts", "manifest.json"))
	if err := registerNoFollow(wt2, func([]byte) ([]byte, error) { return []byte("{}"), nil }); err == nil {
		t.Fatal("symlinked manifest must fail")
	}
	wt3 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(wt3, ".artifacts"), 0o755)
	_ = os.Symlink(filepath.Join(outside, "l"), filepath.Join(wt3, ".artifacts", artifactpkg.LockFileName))
	if err := registerNoFollow(wt3, func([]byte) ([]byte, error) { return []byte("{}"), nil }); err == nil {
		t.Fatal("symlinked lock file must fail")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the worktree: %v", entries)
	}
}

// Race: flip .artifacts between a real directory and a symlink to an outside
// directory while uploads register. Nothing may ever be written outside.
func TestUploadRegistrationSymlinkRace(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	outside := t.TempDir()
	art := filepath.Join(s.Path, ".artifacts")
	real := filepath.Join(s.Path, ".artifacts-real")
	if err := os.MkdirAll(filepath.Join(art, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_ = os.Rename(art, real)
			_ = os.Symlink(outside, art)
			_ = os.Remove(art)
			_ = os.Rename(real, art)
		}
	}()
	for i := 0; i < 40; i++ {
		_, _ = e.svc.Upload(uploadReq(a.ID, fmt.Sprintf("k%d", i), fmt.Sprintf("f%d.txt", i), "text/plain", []byte("x"), 0, true))
	}
	stop.Store(true)
	wg.Wait()
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("upload wrote outside the worktree: %v", names)
	}
}

// Review finding: completed attachments are bounded per session.
func TestUploadSessionQuota(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	// Pre-register MaxSessionAttachments-1 remote attachments directly.
	m := artifactpkg.NewManifest(s.Name)
	_ = os.MkdirAll(filepath.Join(s.Path, ".artifacts", "attachments"), 0o755)
	for i := 0; i < MaxSessionAttachments-1; i++ {
		name := fmt.Sprintf("attachments/p%d.txt", i)
		_ = os.WriteFile(filepath.Join(s.Path, ".artifacts", name), []byte("p"), 0o644)
		m.Artifacts = append(m.Artifacts, artifactpkg.Artifact{ID: fmt.Sprintf("p%d", i), Type: "other", Title: "p", File: name,
			Folder: "attachments", Created: e.now, Retention: "session", Tags: []string{AttachmentTag}})
	}
	if err := artifactpkg.SaveManifest(s, m); err != nil {
		t.Fatal(err)
	}
	// One more fits, and registering it keeps the manifest at 0600.
	_ = os.Chmod(filepath.Join(s.Path, ".artifacts", "manifest.json"), 0o600)
	e.uploadAll(a.ID, "last", "last.txt", "text/plain", []byte("ok"))
	if fi, err := os.Stat(filepath.Join(s.Path, ".artifacts", "manifest.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode after remote upload: %v %v", fi.Mode(), err)
	}
	// The next is refused, writes nothing, and does not leave the key stuck.
	before, _ := os.ReadDir(filepath.Join(s.Path, ".artifacts", "attachments"))
	_, err := e.svc.Upload(uploadReq(a.ID, "over", "over.txt", "text/plain", []byte("no"), 0, true))
	if codeOf(err) != codeLimit {
		t.Fatalf("over quota: %v", err)
	}
	after, _ := os.ReadDir(filepath.Join(s.Path, ".artifacts", "attachments"))
	if len(after) != len(before) {
		t.Fatalf("refused upload left a file: %d -> %d", len(before), len(after))
	}
	if n := e.svc.pendingCount(e.svc.uploadsDir(a.ID)); n != 0 {
		t.Fatalf("refused upload left staged bytes: %d", n)
	}
	// Non-attachment artifacts do not count against the quota.
	m2, _ := artifactpkg.LoadManifest(s)
	m2.Artifacts = m2.Artifacts[:0]
	_ = artifactpkg.SaveManifest(s, m2)
	e.register(s, "Own", "own.md", bytes.Repeat([]byte("x"), 10))
	e.uploadAll(a.ID, "after", "after.txt", "text/plain", []byte("ok"))
}

// Review finding: a failed read-back must not leave the key stuck.
func TestUploadReadBackFailureDoesNotWedgeKey(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	data := []byte("reference")
	// Stage everything, then simulate the stored file differing by making
	// the existing-entry adoption path see different content.
	if _, err := e.svc.Upload(uploadReq(a.ID, "c", "c.txt", "text/plain", data, 0, true)); err != nil {
		t.Fatal(err)
	}
	recPath := filepath.Join(e.svc.uploadsDir(a.ID), keyHash(a.ID, "c")+".json")
	rec, _ := readRecord(recPath)
	m, _ := artifactpkg.LoadManifest(s)
	entry, _ := artifactpkg.Find(m, rec.ManifestID)
	// Crash-recovery state (all bytes staged, completion not recorded) while
	// the registered file no longer matches: verification fails.
	_ = os.WriteFile(filepath.Join(s.Path, ".artifacts", entry.File), []byte("tampered!"), 0o644)
	rec.State, rec.ManifestID = "receiving", ""
	_ = writeRecord(recPath, rec)
	part := filepath.Join(e.svc.uploadsDir(a.ID), keyHash(a.ID, "c")+".part")
	_ = os.WriteFile(part, data, 0o600)
	if _, err := e.svc.Upload(uploadReq(a.ID, "c", "c.txt", "text/plain", data, 0, false)); codeOf(err) != codeConflict {
		t.Fatalf("tampered existing entry: %v", err)
	}
	// The attempt is discarded (no pending slot, no stuck record).
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("staged bytes left behind")
	}
	if _, err := os.Stat(recPath); !os.IsNotExist(err) {
		t.Fatal("upload record left behind")
	}
	// The existing entry is untouched.
	if b, _ := os.ReadFile(filepath.Join(s.Path, ".artifacts", entry.File)); string(b) != "tampered!" {
		t.Fatal("existing entry was modified")
	}
}
