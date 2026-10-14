// Package backuprestore verifies a DevX private backup by restoring it into
// a fixture-owned directory only. It never writes outside that directory
// and never executes anything restored.
//
// Backup layout (produced by create-private-backup.py, schema 1):
//
//	manifest.json          files[], worktrees[], archive_sha256, bundle_sha256
//	private-files.tar.gz   members "files/<absolute source without leading />"
//	devx.git.bundle        `git bundle create --all` of the main repo
//	worktree-NN.patch      `git diff --binary HEAD` per worktree
//
// Safety rules, checked BEFORE any write:
//   - the fixture root must be an existing, empty-or-owned directory with a
//     marker file this package wrote, and must not be (or contain, or be
//     inside) any protected root (real HOME config, project roots, /);
//   - archive/bundle/patch hashes must match the manifest;
//   - every archive member must be a regular file whose name is relative,
//     clean, under "files/", without "..", and listed in the manifest with
//     a matching sha256 and size; links/devices/dirs-as-files are rejected;
//   - every destination is computed by joining the fixture root with the
//     cleaned member name and re-checked to stay inside the root.
//
// Files are written 0600 into 0700 directories regardless of archived mode
// (they are data, not executables).
package backuprestore

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrRejected marks a refusal made before any write.
var ErrRejected = errors.New("backuprestore: rejected")

func rejectf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRejected, fmt.Sprintf(format, a...))
}

// Manifest mirrors the backup manifest (only fields we use).
type Manifest struct {
	Schema        int        `json:"schema"`
	Files         []File     `json:"files"`
	Worktrees     []Worktree `json:"worktrees"`
	ArchiveSHA256 string     `json:"archive_sha256"`
	BundleSHA256  string     `json:"bundle_sha256"`
}

type File struct {
	Source      string `json:"source"`
	ArchivePath string `json:"archive_path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	Mode        int    `json:"mode"`
}

type Worktree struct {
	Worktree       string `json:"worktree"`
	HEAD           string `json:"HEAD"`
	Branch         string `json:"branch"`
	PatchFile      string `json:"patch_file"`
	PatchSHA256    string `json:"patch_sha256"`
	Dirty          bool   `json:"dirty"`
	UntrackedCount int    `json:"untracked_count"`
}

// Options controls a restore.
type Options struct {
	BackupDir string
	// FixtureRoot receives everything. Must be created by NewFixtureRoot.
	FixtureRoot string
	// Protected roots: restore refuses a fixture root equal to, inside, or
	// containing any of these. Callers add the real HOME, config dirs and
	// project roots.
	Protected []string
}

const markerName = ".devx-restore-fixture"

// NewFixtureRoot creates a fresh 0700 directory under base and marks it as
// owned by this package.
func NewFixtureRoot(base string) (string, error) {
	dir, err := os.MkdirTemp(base, "dxrestore-")
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, markerName), []byte("owned by devx backuprestore\n"), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// CheckFixtureRoot validates root against protected roots and the marker.
func CheckFixtureRoot(root string, protected []string) error {
	if root == "" || !filepath.IsAbs(root) {
		return rejectf("fixture root %q must be an absolute path", root)
	}
	clean := filepath.Clean(root)
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		clean = real
	}
	if clean == "/" || clean == filepath.Dir(clean) {
		return rejectf("fixture root %q is a filesystem root", root)
	}
	for _, p := range protected {
		if p == "" {
			continue
		}
		pc := filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(pc); err == nil {
			pc = r
		}
		if clean == pc || within(clean, pc) || within(pc, clean) {
			return rejectf("fixture root %q overlaps protected path %q", root, p)
		}
	}
	st, err := os.Lstat(clean)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return rejectf("fixture root %q is not an existing directory", root)
	}
	if _, err := os.Stat(filepath.Join(clean, markerName)); err != nil {
		return rejectf("fixture root %q was not created by NewFixtureRoot", root)
	}
	return nil
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// DestFor maps an archive member to its path under root, rejecting anything
// that is not a clean relative "files/..." path inside root.
func DestFor(root, member string) (string, error) {
	if member == "" || strings.HasPrefix(member, "/") || strings.Contains(member, "\\") || strings.ContainsRune(member, 0) {
		return "", rejectf("archive name %q is absolute or malformed", member)
	}
	if path.Clean(member) != member {
		return "", rejectf("archive name %q is not clean", member)
	}
	for _, seg := range strings.Split(member, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return "", rejectf("archive name %q contains traversal", member)
		}
	}
	if !strings.HasPrefix(member, "files/") {
		return "", rejectf("archive name %q is outside files/", member)
	}
	dest := filepath.Join(root, "restored", filepath.FromSlash(member))
	if !within(dest, root) {
		return "", rejectf("archive name %q escapes the fixture root", member)
	}
	return dest, nil
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LoadManifest reads and structurally validates the manifest.
func LoadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, rejectf("manifest is not valid JSON")
	}
	if m.Schema != 1 || len(m.ArchiveSHA256) != 64 || len(m.BundleSHA256) != 64 {
		return nil, rejectf("manifest schema/hashes missing")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if _, err := DestFor("/nonexistent-root", f.ArchivePath); err != nil {
			return nil, rejectf("manifest entry: %v", err)
		}
		if seen[f.ArchivePath] || len(f.SHA256) != 64 || f.Size < 0 {
			return nil, rejectf("manifest entry %q duplicated or missing hash", f.ArchivePath)
		}
		seen[f.ArchivePath] = true
	}
	for _, w := range m.Worktrees {
		if w.PatchFile != filepath.Base(w.PatchFile) || !strings.HasPrefix(w.PatchFile, "worktree-") || !strings.HasSuffix(w.PatchFile, ".patch") {
			return nil, rejectf("manifest patch name %q is not a plain worktree-NN.patch", w.PatchFile)
		}
	}
	return &m, nil
}

// VerifyArtifacts checks archive, bundle and patch hashes against the
// manifest. It writes nothing.
func VerifyArtifacts(dir string, m *Manifest) error {
	for name, want := range map[string]string{"private-files.tar.gz": m.ArchiveSHA256, "devx.git.bundle": m.BundleSHA256} {
		got, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != want {
			return rejectf("%s sha256 mismatch", name)
		}
	}
	for _, w := range m.Worktrees {
		got, err := sha256File(filepath.Join(dir, w.PatchFile))
		if err != nil {
			return err
		}
		if got != w.PatchSHA256 {
			return rejectf("%s sha256 mismatch", w.PatchFile)
		}
	}
	return nil
}

// Result reports counts and hashes only.
type Result struct {
	FilesRestored  int    `json:"files_restored"`
	BytesRestored  int64  `json:"bytes_restored"`
	HashMatches    int    `json:"hash_matches"`
	ManifestFiles  int    `json:"manifest_files"`
	RestoredDigest string `json:"restored_set_digest"`
}

// RestoreFiles restores archive members as data under
// <root>/restored/files/... after all pre-write checks pass. The archive is
// streamed twice: a validation pass with no writes, then the write pass.
func RestoreFiles(opts Options) (*Result, error) {
	if err := CheckFixtureRoot(opts.FixtureRoot, opts.Protected); err != nil {
		return nil, err
	}
	m, err := LoadManifest(opts.BackupDir)
	if err != nil {
		return nil, err
	}
	if err := VerifyArtifacts(opts.BackupDir, m); err != nil {
		return nil, err
	}
	entries := map[string]File{}
	for _, f := range m.Files {
		entries[f.ArchivePath] = f
	}
	// Open the archive once and stream both passes from the same file
	// handle, so a swap of the path between passes cannot feed pass 2
	// members that pass 1 never validated.
	af, err := os.Open(filepath.Join(opts.BackupDir, "private-files.tar.gz"))
	if err != nil {
		return nil, err
	}
	defer af.Close()
	// Pass 1: validate every member; no writes.
	seen := map[string]bool{}
	if err := walkTarReader(af, func(h *tar.Header, r io.Reader) error {
		if err := checkMember(opts.FixtureRoot, h, entries); err != nil {
			return err
		}
		if seen[h.Name] {
			return rejectf("duplicate archive member %q", h.Name)
		}
		seen[h.Name] = true
		hs := sha256.New()
		n, err := io.Copy(hs, r)
		if err != nil {
			return err
		}
		e := entries[h.Name]
		if n != e.Size || hex.EncodeToString(hs.Sum(nil)) != e.SHA256 {
			return rejectf("archive member %q content does not match manifest", h.Name)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(seen) != len(entries) {
		return nil, rejectf("archive has %d members, manifest lists %d", len(seen), len(entries))
	}
	// Pass 2: write, from the same handle, after re-checking its digest.
	if err := rewindAndCheck(af, m.ArchiveSHA256); err != nil {
		return nil, err
	}
	res := &Result{ManifestFiles: len(entries)}
	digest := sha256.New()
	written := map[string]bool{}
	err = walkTarReader(af, func(h *tar.Header, r io.Reader) error {
		if !seen[h.Name] || written[h.Name] {
			return rejectf("archive member %q was not validated in pass 1", h.Name)
		}
		written[h.Name] = true
		dest, err := DestFor(opts.FixtureRoot, h.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := ensureNoSymlinkParents(opts.FixtureRoot, filepath.Dir(dest)); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		hs := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, hs), r)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		sum := hex.EncodeToString(hs.Sum(nil))
		if sum != entries[h.Name].SHA256 || n != entries[h.Name].Size {
			return rejectf("archive member %q changed between validation and write", h.Name)
		}
		res.FilesRestored++
		res.BytesRestored += n
		res.HashMatches++
		fmt.Fprintf(digest, "%s %s\n", h.Name, sum)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(written) != len(entries) {
		return nil, rejectf("wrote %d members, manifest lists %d", len(written), len(entries))
	}
	res.RestoredDigest = hex.EncodeToString(digest.Sum(nil))
	return res, nil
}

func checkMember(root string, h *tar.Header, entries map[string]File) error {
	if h.Typeflag != tar.TypeReg {
		return rejectf("archive member %q is not a regular file (type %q)", h.Name, string(h.Typeflag))
	}
	if h.Linkname != "" {
		return rejectf("archive member %q has a link target", h.Name)
	}
	if _, err := DestFor(root, h.Name); err != nil {
		return err
	}
	if _, ok := entries[h.Name]; !ok {
		return rejectf("archive member %q is not in the manifest", h.Name)
	}
	return nil
}

func ensureNoSymlinkParents(root, dir string) error {
	for d := dir; within(d, root); d = filepath.Dir(d) {
		st, err := os.Lstat(d)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return rejectf("restore path %q contains a symlink", d)
		}
	}
	return nil
}

// ensureNoExistingSymlinkParents is ensureNoSymlinkParents for a path whose
// trailing components may not exist yet: missing components are fine,
// existing ones must not be symlinks.
func ensureNoExistingSymlinkParents(root, dir string) error {
	for d := dir; within(d, root); d = filepath.Dir(d) {
		st, err := os.Lstat(d)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return rejectf("restore path %q contains a symlink", d)
		}
	}
	return nil
}

// rewindAndCheck seeks f to the start, verifies its sha256, and seeks back.
func rewindAndCheck(f *os.File, want string) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return rejectf("archive changed between validation and write")
	}
	_, err := f.Seek(0, io.SeekStart)
	return err
}

func walkTarReader(f io.Reader, fn func(*tar.Header, io.Reader) error) error {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}
