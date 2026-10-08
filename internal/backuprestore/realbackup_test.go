package backuprestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoreRealBackupIntoFixture restores the operator's private backup
// into a fixture-owned directory. It runs only when DEVX_RESTORE_BACKUP_DIR
// and DEVX_RESTORE_FIXTURE_BASE are set (never in CI). It reports counts
// and hashes only, to DEVX_RESTORE_RESULT (a private path), and never
// prints restored contents.
func TestRestoreRealBackupIntoFixture(t *testing.T) {
	backup := os.Getenv("DEVX_RESTORE_BACKUP_DIR")
	base := os.Getenv("DEVX_RESTORE_FIXTURE_BASE")
	realHome := os.Getenv("DEVX_RESTORE_REAL_HOME")
	if backup == "" || base == "" || realHome == "" {
		t.Skip("set DEVX_RESTORE_BACKUP_DIR, DEVX_RESTORE_FIXTURE_BASE and DEVX_RESTORE_REAL_HOME to run")
	}
	protected := []string{realHome, filepath.Join(realHome, ".config"), filepath.Join(realHome, ".pi"), filepath.Join(realHome, "projects"), filepath.Join(realHome, "Documents"), backup}
	// The fixture base itself must be private and outside every protected root.
	root, err := NewFixtureRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckFixtureRoot(root, protected); err != nil {
		t.Fatalf("fixture root rejected: %v", err)
	}
	backupBefore := dirDigest(t, backup)

	opts := Options{BackupDir: backup, FixtureRoot: root, Protected: protected}
	res, err := RestoreFiles(opts)
	if err != nil {
		t.Fatalf("restore files: %v", err)
	}
	if res.FilesRestored != res.ManifestFiles || res.HashMatches != res.ManifestFiles {
		t.Fatalf("file restore incomplete: %+v", res)
	}
	m, err := LoadManifest(backup)
	if err != nil {
		t.Fatal(err)
	}
	wts, err := RestoreWorktrees(opts, m)
	if err != nil {
		t.Fatalf("restore worktrees: %v", err)
	}
	rw, err := RewriteForFixture(root, realHome, "synthetic-restore")
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if rw.RealPathRefsAfter != 0 {
		t.Fatalf("real path references remain in fixture config: %d", rw.RealPathRefsAfter)
	}

	// Permissions: every restored file 0600, every dir 0700.
	badPerm := 0
	_ = filepath.Walk(filepath.Join(root, "restored"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Mode().Perm() != 0o700 || !info.IsDir() && info.Mode().Perm() != 0o600 {
			badPerm++
		}
		return nil
	})

	type wtOut struct {
		Index, UntrackedExpected, UntrackedRestored, UntrackedHashOK int
		Name                                                         string
		Dirty, HEADMatches, PatchApplied, DiffMatchesPatch, OK       bool
		Error                                                        string
	}
	okCount, dirtyOK, dirtyTotal := 0, 0, 0
	var outs []wtOut
	for _, w := range wts {
		ok := w.Error == "" && w.HEADMatches && w.DiffMatchesPatch && w.UntrackedHashOK == w.UntrackedRestored
		if ok {
			okCount++
		}
		if w.Dirty {
			dirtyTotal++
			if ok {
				dirtyOK++
			}
		}
		errText := ""
		if w.Error != "" {
			errText = "error (details withheld; see private log)"
		}
		outs = append(outs, wtOut{w.Index, w.UntrackedExpected, w.UntrackedRestored, w.UntrackedHashOK, w.Name, w.Dirty, w.HEADMatches, w.PatchApplied, w.DiffMatchesPatch, ok, errText})
	}
	backupAfter := dirDigest(t, backup)
	result := map[string]any{
		"fixture_root":                   root,
		"files":                          res,
		"worktrees_total":                len(wts),
		"worktrees_fidelity_ok":          okCount,
		"dirty_worktrees_total":          dirtyTotal,
		"dirty_worktrees_fidelity_ok":    dirtyOK,
		"worktrees":                      outs,
		"rewrite":                        rw,
		"restored_permission_violations": badPerm,
		"backup_dir_digest_before":       backupBefore,
		"backup_dir_digest_after":        backupAfter,
		"backup_unchanged":               backupBefore == backupAfter,
		"executed_restored_content":      false,
	}
	if out := os.Getenv("DEVX_RESTORE_RESULT"); out != "" {
		data, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(out, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if backupBefore != backupAfter {
		t.Fatal("backup directory changed during restore")
	}
	if badPerm != 0 {
		t.Fatalf("%d restored paths have non-private permissions", badPerm)
	}
	if okCount != len(wts) {
		t.Errorf("worktree fidelity %d/%d (dirty %d/%d)", okCount, len(wts), dirtyOK, dirtyTotal)
		for _, w := range wts {
			if w.Error != "" {
				// Error strings come from git and may include file names
				// from the private repo: log only to the private test log.
				t.Logf("wt-%02d: %s", w.Index, firstLine(w.Error))
			}
		}
	}
}

// dirDigest hashes names+sizes+mtimes+contents of the backup files.
func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fh := sha256.New()
		_, _ = ioCopy(fh, f)
		_ = f.Close()
		h.Write([]byte(e.Name() + " " + info.ModTime().UTC().String() + " " + info.Mode().String() + " " + hex.EncodeToString(fh.Sum(nil)) + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func firstLine(s string) string { l, _, _ := strings.Cut(s, "\n"); return l }

func ioCopy(dst interface{ Write([]byte) (int, error) }, src *os.File) (int64, error) {
	buf := make([]byte, 1<<20)
	var n int64
	for {
		k, err := src.Read(buf)
		if k > 0 {
			_, _ = dst.Write(buf[:k])
			n += int64(k)
		}
		if err != nil {
			return n, nil
		}
	}
}
