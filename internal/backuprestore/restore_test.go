package backuprestore

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// Same guard as cmd/session/web: any tmux exec reached from these tests is
// pinned to an owned socket and a fake HOME. (Restore code never runs tmux.)
func TestMain(m *testing.M) { os.Exit(tmuxfixture.RunGuarded(m)) }

// --- synthetic backups (no real data) ------------------------------------

type member struct {
	name     string
	body     string
	typeflag byte
	link     string
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func fileSum(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// makeBackup writes a synthetic backup dir. manifestFiles defaults to the
// regular members. mutate may tamper with the manifest before writing.
func makeBackup(t *testing.T, members []member, mutate func(*Manifest)) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "private-files.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	m := &Manifest{Schema: 1}
	for _, mb := range members {
		tf := mb.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		h := &tar.Header{Name: mb.name, Typeflag: tf, Mode: 0o600, Linkname: mb.link}
		if tf == tar.TypeReg {
			h.Size = int64(len(mb.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if tf == tar.TypeReg {
			_, _ = tw.Write([]byte(mb.body))
		}
		m.Files = append(m.Files, File{Source: "/" + strings.TrimPrefix(mb.name, "files/"), ArchivePath: mb.name, SHA256: sum(mb.body), Size: int64(len(mb.body))})
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	_ = os.WriteFile(filepath.Join(dir, "devx.git.bundle"), []byte("bundle"), 0o600)
	m.ArchiveSHA256 = fileSum(t, filepath.Join(dir, "private-files.tar.gz"))
	m.BundleSHA256 = sum("bundle")
	if mutate != nil {
		mutate(m)
	}
	data, _ := json.Marshal(m)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600)
	return dir
}

func newRoot(t *testing.T) string {
	t.Helper()
	root, err := NewFixtureRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// snapshot lists every path under dir (to prove "no writes").
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil {
			paths = append(paths, strings.TrimPrefix(p, dir))
		}
		return nil
	})
	return strings.Join(paths, "\n")
}

func mustReject(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("%s: want ErrRejected, got %v", what, err)
	}
}

func TestRestoreHappyPathWritesOnlyUnderFixture(t *testing.T) {
	b := makeBackup(t, []member{{name: "files/Users/x/.config/devx/sessions.json", body: `{"sessions":{}}`}, {name: "files/Users/x/proj/.devx/config.yaml", body: "a: 1\n"}}, nil)
	root := newRoot(t)
	res, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesRestored != 2 || res.HashMatches != 2 || res.ManifestFiles != 2 {
		t.Fatalf("%+v", res)
	}
	p := filepath.Join(root, "restored", "files", "Users", "x", ".config", "devx", "sessions.json")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Mode bits are only enforced on Unix; Windows reports 0666 for every
	// writable file (ACLs control access there).
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("restored file perms: %v", st.Mode())
	}
}

func TestRejectTraversalAbsoluteAndUnlistedNames(t *testing.T) {
	cases := map[string]member{
		"dotdot":        {name: "files/../../etc/passwd", body: "x"},
		"absolute":      {name: "/etc/passwd", body: "x"},
		"outside files": {name: "other/x", body: "x"},
		"dot segment":   {name: "files/./x", body: "x"},
		"backslash":     {name: "files\\..\\x", body: "x"},
	}
	for name, mb := range cases {
		t.Run(name, func(t *testing.T) {
			b := makeBackup(t, []member{mb}, nil)
			root := newRoot(t)
			before := snapshot(t, root)
			_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
			mustReject(t, err, name)
			if snapshot(t, root) != before {
				t.Fatal("fixture modified by rejected restore")
			}
		})
	}
	// Member present in archive but not in manifest.
	b := makeBackup(t, []member{{name: "files/a", body: "a"}, {name: "files/b", body: "b"}}, func(m *Manifest) { m.Files = m.Files[:1] })
	root := newRoot(t)
	before := snapshot(t, root)
	_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
	mustReject(t, err, "unlisted member")
	if snapshot(t, root) != before {
		t.Fatal("fixture modified")
	}
}

func TestRejectSymlinkHardlinkAndDeviceMembers(t *testing.T) {
	for name, mb := range map[string]member{
		"symlink":  {name: "files/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"},
		"hardlink": {name: "files/hl", typeflag: tar.TypeLink, link: "files/x"},
		"chardev":  {name: "files/dev", typeflag: tar.TypeChar},
		"dir":      {name: "files/d", typeflag: tar.TypeDir},
	} {
		t.Run(name, func(t *testing.T) {
			b := makeBackup(t, []member{{name: "files/ok", body: "ok"}, mb}, nil)
			root := newRoot(t)
			before := snapshot(t, root)
			_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
			mustReject(t, err, name)
			if snapshot(t, root) != before {
				t.Fatal("fixture modified by rejected restore (ok member must not be written either)")
			}
		})
	}
}

func TestRejectChecksumAndManifestTampering(t *testing.T) {
	good := []member{{name: "files/a", body: "alpha"}}
	cases := map[string]func(*Manifest){
		"member sha mismatch":  func(m *Manifest) { m.Files[0].SHA256 = sum("other") },
		"member size mismatch": func(m *Manifest) { m.Files[0].Size = 99 },
		"archive sha mismatch": func(m *Manifest) { m.ArchiveSHA256 = sum("x") },
		"bundle sha mismatch":  func(m *Manifest) { m.BundleSHA256 = sum("x") },
		"manifest traversal":   func(m *Manifest) { m.Files[0].ArchivePath = "files/../../x" },
		"manifest absolute":    func(m *Manifest) { m.Files[0].ArchivePath = "/x" },
		"patch name traversal": func(m *Manifest) { m.Worktrees = []Worktree{{PatchFile: "../../etc/x.patch"}} },
		"wrong schema":         func(m *Manifest) { m.Schema = 2 },
		"duplicate entries":    func(m *Manifest) { m.Files = append(m.Files, m.Files[0]) },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			b := makeBackup(t, good, mut)
			root := newRoot(t)
			before := snapshot(t, root)
			_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
			mustReject(t, err, name)
			if snapshot(t, root) != before {
				t.Fatal("fixture modified")
			}
		})
	}
	// Archive bytes modified after manifest was written.
	b := makeBackup(t, good, nil)
	f, _ := os.OpenFile(filepath.Join(b, "private-files.tar.gz"), os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.Write([]byte("tamper"))
	_ = f.Close()
	_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: newRoot(t)})
	mustReject(t, err, "archive bytes tampered")
	// Malformed manifest.
	b = makeBackup(t, good, nil)
	_ = os.WriteFile(filepath.Join(b, "manifest.json"), []byte("{not json"), 0o600)
	_, err = RestoreFiles(Options{BackupDir: b, FixtureRoot: newRoot(t)})
	mustReject(t, err, "malformed manifest")
}

// Malicious destinations: the fixture root itself is rejected when it is
// a real/default/config root, overlaps a protected root, is relative, is a
// symlink, or was not created by NewFixtureRoot. No writes happen.
func TestRejectMaliciousFixtureRoots(t *testing.T) {
	b := makeBackup(t, []member{{name: "files/a", body: "a"}}, nil)
	realHome := "/Users/jfox"
	protected := []string{realHome, filepath.Join(realHome, ".config", "devx"), "/Users/jfox/projects"}
	tmp := t.TempDir()
	unmarked := filepath.Join(tmp, "unmarked")
	_ = os.Mkdir(unmarked, 0o700)
	marked := newRoot(t)
	link := filepath.Join(tmp, "link")
	_ = os.Symlink(marked, link)
	cases := map[string]string{
		"real config dir":    filepath.Join(realHome, ".config", "devx"),
		"real home":          realHome,
		"inside protected":   filepath.Join(realHome, ".config", "devx", "x"),
		"contains protected": "/Users",
		"filesystem root":    "/",
		"relative":           "relative/dir",
		"empty":              "",
		"unmarked dir":       unmarked,
		"default tmux dir":   "/private/tmp/tmux-501",
	}
	for name, root := range cases {
		_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root, Protected: protected})
		mustReject(t, err, name)
	}
	if entries, _ := os.ReadDir(unmarked); len(entries) != 0 {
		t.Fatal("unmarked dir was written")
	}
	// Symlinked fixture root resolving into a protected path is rejected;
	// a symlink to an owned root is resolved and accepted.
	protLink := filepath.Join(tmp, "protlink")
	_ = os.Symlink(filepath.Join(realHome, ".config", "devx"), protLink)
	_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: protLink, Protected: protected})
	mustReject(t, err, "symlink into protected")
	if _, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: link, Protected: protected}); err != nil {
		t.Fatalf("symlink to owned root should resolve: %v", err)
	}
}

// A symlink planted inside the fixture (e.g. by a hostile earlier member or
// a racing process) must not be followed.
func TestRejectPlantedSymlinkInsideFixture(t *testing.T) {
	b := makeBackup(t, []member{{name: "files/evil/x", body: "x"}}, nil)
	root := newRoot(t)
	outside := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "restored", "files"), 0o700)
	_ = os.Symlink(outside, filepath.Join(root, "restored", "files", "evil"))
	_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
	mustReject(t, err, "planted symlink")
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("write followed the planted symlink outside the fixture")
	}
}

// CodeRabbit r4238379172: a planted symlink deeper than the direct parent
// must be rejected BEFORE MkdirAll can create anything through it.
func TestRejectDeepPlantedSymlinkCreatesNothingOutside(t *testing.T) {
	b := makeBackup(t, []member{{name: "files/evil/sub/deeper/x", body: "x"}}, nil)
	root := newRoot(t)
	outside := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "restored", "files"), 0o700)
	_ = os.Symlink(outside, filepath.Join(root, "restored", "files", "evil"))
	_, err := RestoreFiles(Options{BackupDir: b, FixtureRoot: root})
	mustReject(t, err, "planted symlink")
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("MkdirAll created %d entries outside the fixture through the symlink", len(entries))
	}
}

func TestDestForAlwaysInsideRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "r")
	for _, ok := range []string{"files/a", "files/a/b/c.json"} {
		d, err := DestFor(root, ok)
		if err != nil || !strings.HasPrefix(d, filepath.Join(root, "restored", "files")+string(filepath.Separator)) {
			t.Fatalf("%s -> %s %v", ok, d, err)
		}
	}
	for _, bad := range []string{"", "/a", "files/../../a", "files//a", "a", "files/a/../../..", "files/a\x00b"} {
		if _, err := DestFor(root, bad); !errors.Is(err, ErrRejected) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGitRunRefusesOutsideFixture(t *testing.T) {
	root := newRoot(t)
	g := Git{Root: root}
	if _, err := g.Run("/Users/jfox/projects/devx", "status"); !errors.Is(err, ErrRejected) {
		t.Fatalf("git outside fixture: %v", err)
	}
	for _, kv := range g.env() {
		if strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "HOME="+root) {
			t.Fatalf("git HOME not inside fixture: %s", kv)
		}
	}
}

func TestRewriteRemovesRealPathsAndConfig(t *testing.T) {
	requireUnixHomeLayout(t)
	root := newRoot(t)
	cfgDir := filepath.Join(root, "restored", "files", "Users", "x", ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "sessions.json"), []byte(`{"sessions":{"s":{"path":"/Users/x/p/.worktrees/s","project_path":"/Users/x/p","other":"/workspace"}}}`), 0o600)
	_ = os.WriteFile(filepath.Join(cfgDir, "projects.json"), []byte(`{"projects":{"p":{"path":"/Users/x/p"}}}`), 0o600)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("web_secret_token: SECRET\n"), 0o600)
	res, err := RewriteForFixture(root, "/Users/x", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if res.RealPathRefsAfter != 0 || res.SessionsRewritten != 1 || res.ProjectsRewritten != 1 {
		t.Fatalf("%+v", res)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, "home", ".config", "devx", "config.yaml"))
	if strings.Contains(string(cfg), "SECRET") || !strings.Contains(string(cfg), `allowed_projects: ["synthetic"]`) {
		t.Fatalf("config not sanitized")
	}
	s, _ := os.ReadFile(filepath.Join(root, "home", ".config", "devx", "sessions.json"))
	if strings.Contains(string(s), `"/Users/x`) || strings.Contains(string(s), `"/workspace"`) {
		t.Fatalf("real paths remain: %s", s)
	}
}

// Hostile project aliases cannot inject YAML keys into the fixture config.
func TestRewriteQuotesAllowProject(t *testing.T) {
	requireUnixHomeLayout(t)
	root := newRoot(t)
	cfgDir := filepath.Join(root, "restored", "files", "Users", "x", ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "sessions.json"), []byte(`{"sessions":{}}`), 0o600)
	_ = os.WriteFile(filepath.Join(cfgDir, "projects.json"), []byte(`{"projects":{}}`), 0o600)
	if _, err := RewriteForFixture(root, "/Users/x", "a], web_secret_token: X\nevil: [1"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, "home", ".config", "devx", "config.yaml"))
	var parsed map[string]any
	if err := yaml.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("config not valid YAML: %v\n%s", err, cfg)
	}
	if _, ok := parsed["evil"]; ok {
		t.Fatalf("alias injected a key:\n%s", cfg)
	}
	if _, ok := parsed["web_secret_token"]; ok {
		t.Fatalf("alias injected a key:\n%s", cfg)
	}
}

// writeNewFileNoFollow never writes through a symlink at the final
// component, and ensureNoExistingSymlinkParents rejects a symlinked parent.
func TestUntrackedWriteRefusesSymlinks(t *testing.T) {
	root := newRoot(t)
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureNoExistingSymlinkParents(root, filepath.Join(link, "sub")); err == nil {
		t.Fatal("symlinked parent accepted")
	}
	if err := ensureNoExistingSymlinkParents(root, filepath.Join(root, "missing", "deeper")); err != nil {
		t.Fatalf("missing components should be fine: %v", err)
	}
	target := filepath.Join(root, "f")
	if err := os.Symlink(filepath.Join(outside, "victim"), target); err != nil {
		t.Fatal(err)
	}
	if err := writeNewFileNoFollow(target, []byte("x")); err == nil {
		t.Fatal("wrote through a final-component symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "victim")); !os.IsNotExist(err) {
		t.Fatal("file created outside the fixture")
	}
}

// requireUnixHomeLayout skips config-rewrite tests on Windows. Reason: the
// verifier restores backups made by create-private-backup.py on macOS, whose
// archive members and config values are absolute Unix paths ("/Users/x/..."),
// and RewriteForFixture requires the real home to be such a path; a Windows
// home ("C:\\Users\\x") is not a supported backup source.
func requireUnixHomeLayout(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("backup rewrite handles macOS/Unix home paths only (backups are made on macOS)")
	}
}
