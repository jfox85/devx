package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/jfox85/devx/session"
)

const (
	ManifestVersion  = 1
	DirName          = ".artifacts"
	ManifestName     = "manifest.json"
	DefaultAgent     = "unknown"
	DefaultRetention = "session"
	ArchiveRetention = "archive"
)

var validTypes = map[string]bool{
	"plan":       true,
	"report":     true,
	"screenshot": true,
	"recording":  true,
	"log":        true,
	"diff":       true,
	"document":   true,
	"other":      true,
}

var validRetentions = map[string]bool{
	DefaultRetention: true,
	ArchiveRetention: true,
}

// Manifest indexes all artifacts for one devx session.
type Manifest struct {
	Version   int        `json:"version"`
	Session   string     `json:"session"`
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact describes one file under a session's .artifacts directory.
type Artifact struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	File      string    `json:"file"`
	Folder    string    `json:"folder,omitempty"`
	Created   time.Time `json:"created"`
	Agent     string    `json:"agent,omitempty"`
	Retention string    `json:"retention,omitempty"`
	Summary   *string   `json:"summary,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Assets    []string  `json:"assets,omitempty"`
	Focus     bool      `json:"focus,omitempty"`
}

// ListItem is the agent/web-facing artifact shape with computed paths.
type ListItem struct {
	Artifact
	Path string `json:"path"`
	URL  string `json:"url"`
}

func DirForSession(sess *session.Session) string {
	return filepath.Join(sess.Path, DirName)
}

func ManifestPath(sess *session.Session) string {
	return filepath.Join(DirForSession(sess), ManifestName)
}

func NewManifest(sessionName string) *Manifest {
	return &Manifest{Version: ManifestVersion, Session: sessionName, Artifacts: []Artifact{}}
}

func LoadManifest(sess *session.Session) (*Manifest, error) {
	path := ManifestPath(sess)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return NewManifest(sess.Name), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read artifact manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse artifact manifest: %w", err)
	}
	if m.Version == 0 {
		m.Version = ManifestVersion
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("unsupported artifact manifest version %d", m.Version)
	}
	if m.Session == "" {
		m.Session = sess.Name
	}
	if m.Session != sess.Name {
		return nil, fmt.Errorf("artifact manifest session %q does not match %q", m.Session, sess.Name)
	}
	if m.Artifacts == nil {
		m.Artifacts = []Artifact{}
	}
	if err := ValidateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func SaveManifest(sess *session.Session, m *Manifest) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	m.Version = ManifestVersion
	m.Session = sess.Name
	if m.Artifacts == nil {
		m.Artifacts = []Artifact{}
	}
	if err := ValidateManifest(m); err != nil {
		return err
	}
	dir := DirForSession(sess)
	if err := EnsureArtifactDir(dir); err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal artifact manifest: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp manifest: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp manifest: %w", err)
	}
	if err := os.Rename(tmpName, ManifestPath(sess)); err != nil {
		return fmt.Errorf("failed to replace artifact manifest: %w", err)
	}
	return nil
}

func ValidateManifest(m *Manifest) error {
	seen := map[string]bool{}
	for i := range m.Artifacts {
		if err := normalizeArtifact(&m.Artifacts[i]); err != nil {
			return err
		}
		a := m.Artifacts[i]
		if err := ValidateArtifact(a); err != nil {
			return err
		}
		if seen[a.ID] {
			return fmt.Errorf("duplicate artifact id %q", a.ID)
		}
		seen[a.ID] = true
	}
	return nil
}

func normalizeArtifact(a *Artifact) error {
	if strings.TrimSpace(a.Folder) == "" {
		return nil
	}
	folder, err := NormalizeFolderPath(a.Folder)
	if err != nil {
		return fmt.Errorf("invalid artifact folder %q: %w", a.Folder, err)
	}
	a.Folder = folder
	return nil
}

func ValidateArtifact(a Artifact) error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("artifact id is required")
	}
	if err := ValidateType(a.Type); err != nil {
		return err
	}
	if strings.TrimSpace(a.Title) == "" {
		return fmt.Errorf("artifact title is required")
	}
	if err := ValidateRelativePath(a.File); err != nil {
		return fmt.Errorf("invalid artifact file %q: %w", a.File, err)
	}
	if strings.TrimSpace(a.Folder) != "" {
		folder, err := NormalizeFolderPath(a.Folder)
		if err != nil {
			return fmt.Errorf("invalid artifact folder %q: %w", a.Folder, err)
		}
		file := filepath.ToSlash(filepath.Clean(a.File))
		if file != folder && !strings.HasPrefix(file, folder+"/") {
			return fmt.Errorf("artifact file %q is not under folder %q", a.File, folder)
		}
	}
	if a.Created.IsZero() {
		return fmt.Errorf("artifact created time is required")
	}
	retention := a.Retention
	if retention == "" {
		retention = DefaultRetention
	}
	if err := ValidateRetention(retention); err != nil {
		return err
	}
	return nil
}

func ValidateType(t string) error {
	if !validTypes[t] {
		return fmt.Errorf("invalid artifact type %q", t)
	}
	return nil
}

func ValidateRetention(r string) error {
	if !validRetentions[r] {
		return fmt.Errorf("invalid artifact retention %q", r)
	}
	return nil
}

func ValidateRelativePath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("absolute paths are not allowed")
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path traversal is not allowed")
	}
	for _, part := range strings.FieldsFunc(clean, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." || part == "" {
			return fmt.Errorf("path traversal is not allowed")
		}
	}
	return nil
}

func NormalizeFolderPath(folder string) (string, error) {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "", fmt.Errorf("folder is empty")
	}
	if filepath.IsAbs(folder) || hasWindowsVolumeName(folder) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	slash := strings.ReplaceAll(folder, "\\", "/")
	if strings.HasPrefix(slash, "/") {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	parts := strings.Split(slash, "/")
	for _, part := range parts {
		if part == "" {
			return "", fmt.Errorf("empty path segments are not allowed")
		}
		if part == "." || part == ".." {
			return "", fmt.Errorf("path traversal is not allowed")
		}
	}
	clean := filepath.ToSlash(filepath.Clean(slash))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path traversal is not allowed")
	}
	return clean, nil
}

func hasWindowsVolumeName(p string) bool {
	return len(p) >= 2 && p[1] == ':' && unicode.IsLetter(rune(p[0]))
}

func SafeJoin(base, rel string) (string, error) {
	if err := ValidateRelativePath(rel); err != nil {
		return "", err
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(baseAbs, filepath.Clean(rel))
	joinedAbs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}
	if joinedAbs != baseAbs && !strings.HasPrefix(joinedAbs, baseAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes artifact directory")
	}
	return joinedAbs, nil
}

func Find(m *Manifest, id string) (*Artifact, int) {
	for i := range m.Artifacts {
		if m.Artifacts[i].ID == id {
			return &m.Artifacts[i], i
		}
	}
	return nil, -1
}

func SortNewestFirst(items []Artifact) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Created.After(items[j].Created)
	})
}

func WithComputedFields(sessionName string, artifacts []Artifact) []ListItem {
	items := make([]ListItem, 0, len(artifacts))
	for _, a := range artifacts {
		items = append(items, ListItem{
			Artifact: a,
			Path:     filepath.ToSlash(filepath.Join(DirName, a.File)),
			URL:      WebPath(sessionName, a.File),
		})
	}
	return items
}
