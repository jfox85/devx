package backuprestore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Git runs git with an environment that cannot reach the user's config,
// hooks, credentials or other repositories: HOME and XDG point inside the
// fixture, system/global config are disabled, GIT_* routing variables are
// removed, and hooks are pointed at an empty fixture directory.
type Git struct {
	Root string // fixture root
}

func (g Git) env() []string {
	out := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_") || k == "HOME" || strings.HasPrefix(k, "XDG_") {
			continue
		}
		out = append(out, kv)
	}
	home := filepath.Join(g.Root, "git-home")
	return append(out,
		"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=restore", "GIT_AUTHOR_EMAIL=restore@fixture.invalid",
		"GIT_COMMITTER_NAME=restore", "GIT_COMMITTER_EMAIL=restore@fixture.invalid")
}

// Run executes git in dir, which must be inside the fixture root.
func (g Git) Run(dir string, args ...string) (string, error) {
	if dir != g.Root && !within(dir, g.Root) {
		return "", rejectf("git dir %q is outside the fixture root", dir)
	}
	hooks := filepath.Join(g.Root, "git-home", "no-hooks")
	_ = os.MkdirAll(hooks, 0o700)
	full := append([]string{"-c", "core.hooksPath=" + hooks, "-c", "protocol.allow=never", "-c", "protocol.file.allow=always"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = g.env()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// WorktreeResult is per-worktree fidelity, hashes and counts only.
type WorktreeResult struct {
	Index             int    `json:"index"`
	Name              string `json:"name"` // basename of the original worktree
	HEADMatches       bool   `json:"head_matches"`
	Dirty             bool   `json:"dirty"`
	PatchApplied      bool   `json:"patch_applied"`
	DiffMatchesPatch  bool   `json:"diff_matches_patch"`
	UntrackedExpected int    `json:"untracked_expected"`
	UntrackedRestored int    `json:"untracked_restored"`
	UntrackedHashOK   int    `json:"untracked_hash_ok"`
	TrackedTreeDigest string `json:"restored_tree_digest"`
	Error             string `json:"error,omitempty"`
}

// RestoreWorktrees clones the bundle under <root>/git/repo, then for each
// manifest worktree checks out its HEAD into <root>/git/wt-NN, applies the
// patch, copies untracked files from the already-restored archive data, and
// verifies `git diff --binary HEAD` reproduces the patch byte-for-byte.
func RestoreWorktrees(opts Options, m *Manifest) ([]WorktreeResult, error) {
	if err := CheckFixtureRoot(opts.FixtureRoot, opts.Protected); err != nil {
		return nil, err
	}
	g := Git{Root: opts.FixtureRoot}
	gitDir := filepath.Join(opts.FixtureRoot, "git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return nil, err
	}
	repo := filepath.Join(gitDir, "repo")
	bundle := filepath.Join(opts.BackupDir, "devx.git.bundle")
	// The bundle is read-only input; clone writes only under the fixture.
	if _, err := g.Run(gitDir, "clone", "--quiet", "--no-checkout", bundle, repo); err != nil {
		return nil, err
	}
	if _, err := g.Run(repo, "config", "core.hooksPath", filepath.Join(opts.FixtureRoot, "git-home", "no-hooks")); err != nil {
		return nil, err
	}
	// Index untracked restored files by original source path.
	bySource := map[string]File{}
	for _, f := range m.Files {
		bySource[f.Source] = f
	}
	// Assign each source file to its MOST SPECIFIC worktree, so files of a
	// nested worktree (<repo>/.worktrees/x/...) are not counted for <repo>.
	owner := map[string]string{}
	for src := range bySource {
		best := ""
		for _, w := range m.Worktrees {
			pre := strings.TrimSuffix(w.Worktree, "/") + "/"
			if strings.HasPrefix(src, pre) && len(pre) > len(best) {
				best = pre
			}
		}
		owner[src] = best
	}
	var out []WorktreeResult
	for i, w := range m.Worktrees {
		r := WorktreeResult{Index: i, Name: filepath.Base(w.Worktree), Dirty: w.Dirty, UntrackedExpected: w.UntrackedCount}
		dst := filepath.Join(gitDir, fmt.Sprintf("wt-%02d", i))
		mine := map[string]File{}
		pre := strings.TrimSuffix(w.Worktree, "/") + "/"
		for src, f := range bySource {
			if owner[src] == pre {
				mine[src] = f
			}
		}
		if err := restoreOne(g, repo, dst, opts, w, mine, &r); err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

func restoreOne(g Git, repo, dst string, opts Options, w Worktree, bySource map[string]File, r *WorktreeResult) error {
	if len(w.HEAD) != 40 {
		return rejectf("worktree HEAD is not a full sha")
	}
	if _, err := g.Run(repo, "worktree", "add", "--quiet", "--detach", dst, w.HEAD); err != nil {
		return err
	}
	head, err := g.Run(dst, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	r.HEADMatches = head == w.HEAD
	patch, err := os.ReadFile(filepath.Join(opts.BackupDir, w.PatchFile))
	if err != nil {
		return err
	}
	if len(patch) > 0 {
		pf := filepath.Join(opts.FixtureRoot, "git", filepath.Base(dst)+".patch")
		if err := os.WriteFile(pf, patch, 0o600); err != nil {
			return err
		}
		if _, err := g.Run(dst, "apply", "--binary", "--whitespace=nowarn", pf); err != nil {
			return err
		}
		r.PatchApplied = true
	}
	diff, err := diffBinary(g, dst)
	if err != nil {
		return err
	}
	r.DiffMatchesPatch = bytes.Equal(diff, patch)
	// Untracked files: restored archive data whose source is under the
	// original worktree path and not tracked at HEAD.
	prefix := strings.TrimSuffix(w.Worktree, "/") + "/"
	var rels []string
	for src := range bySource {
		if strings.HasPrefix(src, prefix) {
			rels = append(rels, strings.TrimPrefix(src, prefix))
		}
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if rel == "" || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
			continue
		}
		if _, err := g.Run(dst, "ls-files", "--error-unmatch", "--", rel); err == nil {
			continue // tracked file (e.g. .devx config); covered by HEAD+patch
		}
		f := bySource[prefix+rel]
		src, err := DestFor(opts.FixtureRoot, f.ArchivePath)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if !within(target, dst) {
			return rejectf("untracked path %q escapes the restored worktree", rel)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		// The checkout and patch can create tracked symlinks; never let a
		// write follow one out of the restored worktree.
		if err := ensureNoExistingSymlinkParents(dst, filepath.Dir(target)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := ensureNoSymlinkParents(dst, filepath.Dir(target)); err != nil {
			return err
		}
		if err := writeNewFileNoFollow(target, data); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		ignored := false
		if _, err := g.Run(dst, "check-ignore", "-q", "--", rel); err == nil {
			ignored = true // e.g. .devx-web-token: backed up but git-ignored
		}
		if !ignored {
			r.UntrackedRestored++
			if hex.EncodeToString(sum[:]) == f.SHA256 {
				r.UntrackedHashOK++
			}
		}
	}
	// Digest of tracked content after patch, for cross-run comparison.
	tree, err := g.Run(dst, "stash", "create")
	if err != nil {
		return err
	}
	if tree == "" {
		tree = head
	}
	treeSHA, err := g.Run(dst, "rev-parse", tree+"^{tree}")
	if err != nil {
		return err
	}
	r.TrackedTreeDigest = treeSHA
	return nil
}

func diffBinary(g Git, dir string) ([]byte, error) {
	cmd := exec.Command("git", "-c", "core.hooksPath=/dev/null", "diff", "--binary", "HEAD")
	cmd.Dir = dir
	cmd.Env = g.env()
	return cmd.Output()
}
