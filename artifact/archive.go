package artifact

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jfox85/devx/session"
)

var (
	htmlAssetRe     = regexp.MustCompile(`(?i)(?:src|href)=["']([^"'#?:][^"']*)["']`)
	markdownAssetRe = regexp.MustCompile(`!?\[[^\]]*\]\(([^)#:?]+)\)`)
	cssAssetRe      = regexp.MustCompile(`(?i)(?:url\(["']?([^"')#?:][^"')]*)["']?\)|@import\s+["']([^"'#?:][^"']*)["'])`)
)

func ArchiveSessionArtifacts(sess *session.Session) (archiveDir string, count int, err error) {
	err = withManifestLock(sess, func() error {
		manifest, err := LoadManifest(sess)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		var archived []Artifact
		files := map[string]bool{}
		for _, a := range manifest.Artifacts {
			if a.Retention != ArchiveRetention {
				continue
			}
			archived = append(archived, a)
			files[filepath.ToSlash(a.File)] = true
			for _, ref := range a.Assets {
				files[ref] = true
			}
		}
		if len(archived) == 0 {
			return nil
		}
		if info, err := os.Lstat(filepath.Join(DirForSession(sess), "theme.css")); err == nil && info.Mode()&os.ModeSymlink == 0 {
			files["theme.css"] = true
		}
		archiveDir, err = uniqueArchiveDir(projectRootForSession(sess), sess.Name, time.Now())
		if err != nil {
			return err
		}
		cleanupDir := archiveDir
		defer func() {
			if cleanupDir != "" {
				_ = os.RemoveAll(cleanupDir)
				archiveDir = ""
			}
		}()
		for rel := range files {
			if err := copyArtifactFile(DirForSession(sess), archiveDir, rel); err != nil {
				return err
			}
		}
		archiveManifest := &Manifest{Version: ManifestVersion, Session: sess.Name, Artifacts: archived}
		data, err := marshalManifest(archiveManifest)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(archiveDir, ManifestName), data, 0o644); err != nil {
			return fmt.Errorf("failed to write archive manifest: %w", err)
		}
		cleanupDir = ""
		count = len(archived)
		return nil
	})
	return archiveDir, count, err
}

func uniqueArchiveDir(projectRoot, sessionName string, now time.Time) (string, error) {
	base := filepath.Join(projectRoot, ".devx", "archive")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	prefix := now.Format("2006-01-02-150405") + "-" + Slugify(sessionName)
	for i := 0; i < 1000; i++ {
		name := prefix
		if i > 0 {
			name = fmt.Sprintf("%s-%d", prefix, i+1)
		}
		candidate := filepath.Join(base, name)
		if err := os.Mkdir(candidate, 0o755); err == nil {
			return candidate, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find unique archive directory for %q", sessionName)
}

func marshalManifest(m *Manifest) ([]byte, error) {
	if err := ValidateManifest(m); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func ReferencedAssets(sess *session.Session, a Artifact) []string {
	return DiscoverAssetBundle(sess, a.File)
}

func DiscoverAssetBundle(sess *session.Session, primary string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(rel string) {
		abs, err := SecureExistingPath(DirForSession(sess), rel)
		if err != nil {
			return
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return
		}
		for _, ref := range ParseReferencedAssets(rel, data) {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			if strings.ToLower(filepath.Ext(ref)) == ".css" {
				visit(ref)
			}
		}
	}
	visit(primary)
	out := make([]string, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	return out
}

func ParseReferencedAssets(file string, data []byte) []string {
	ext := strings.ToLower(filepath.Ext(file))
	if ext != ".html" && ext != ".htm" && ext != ".md" && ext != ".css" {
		return nil
	}
	baseDir := filepath.Dir(filepath.ToSlash(file))
	refs := map[string]bool{}
	collectRef := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.HasPrefix(ref, "/") || strings.Contains(ref, "://") || strings.HasPrefix(ref, "data:") || strings.HasPrefix(ref, "mailto:") {
			return
		}
		ref = strings.TrimPrefix(ref, "./")
		joined := filepath.ToSlash(filepath.Clean(filepath.Join(baseDir, ref)))
		if ValidateRelativePath(joined) == nil {
			refs[joined] = true
		}
	}
	collectMatch := func(match []string) {
		for _, group := range match[1:] {
			if strings.TrimSpace(group) != "" {
				collectRef(group)
				return
			}
		}
	}
	content := string(data)
	if ext == ".html" || ext == ".htm" {
		for _, m := range htmlAssetRe.FindAllStringSubmatch(content, -1) {
			collectMatch(m)
		}
	}
	if ext == ".md" {
		for _, m := range markdownAssetRe.FindAllStringSubmatch(content, -1) {
			collectMatch(m)
		}
	}
	if ext == ".css" {
		for _, m := range cssAssetRe.FindAllStringSubmatch(content, -1) {
			collectMatch(m)
		}
	}
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	return out
}

func copyArtifactFile(srcRoot, dstRoot, rel string) error {
	src, err := SecureExistingPath(srcRoot, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	dst, err := SecureNewPath(dstRoot, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func projectRootForSession(sess *session.Session) string {
	if sess.ProjectPath != "" {
		return sess.ProjectPath
	}
	path := filepath.Clean(sess.Path)
	for {
		if filepath.Base(filepath.Dir(path)) == ".worktrees" {
			return filepath.Dir(filepath.Dir(path))
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return filepath.Dir(sess.Path)
}
