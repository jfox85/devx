package artifact

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jfox85/devx/session"
)

type AddOptions struct {
	Source      string
	Reader      io.Reader
	Destination string
	Folder      string
	ID          string
	Type        string
	Title       string
	Summary     string
	Agent       string
	Retention   string
	Tags        []string
	Focus       bool
	Now         time.Time
}

func Add(sess *session.Session, opts AddOptions) (out Artifact, err error) {
	err = withManifestLock(sess, func() error {
		var addErr error
		out, addErr = addLocked(sess, opts)
		return addErr
	})
	return out, err
}

func addLocked(sess *session.Session, opts AddOptions) (Artifact, error) {
	if strings.TrimSpace(opts.Title) == "" {
		return Artifact{}, fmt.Errorf("title is required")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	artifactType := opts.Type
	if artifactType == "" {
		detectPath := opts.Source
		if opts.Destination != "" {
			detectPath = opts.Destination
		}
		artifactType = DetectType(detectPath)
	}
	if err := ValidateType(artifactType); err != nil {
		return Artifact{}, err
	}
	retention := opts.Retention
	if retention == "" {
		retention = DefaultRetention
	}
	if err := ValidateRetention(retention); err != nil {
		return Artifact{}, err
	}
	agent := opts.Agent
	if agent == "" {
		agent = DefaultAgent
	}
	id := opts.ID
	if id == "" {
		id = GenerateID(artifactType, opts.Title, now)
	}
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, `/\`) {
		return Artifact{}, fmt.Errorf("invalid artifact id %q", id)
	}
	folder := ""
	if strings.TrimSpace(opts.Folder) != "" {
		var err error
		folder, err = NormalizeFolderPath(opts.Folder)
		if err != nil {
			return Artifact{}, fmt.Errorf("invalid folder: %w", err)
		}
	}
	destRel := opts.Destination
	if destRel == "" {
		destRel = DefaultDestination(artifactType, opts.Source)
	}
	if err := ValidateRelativePath(destRel); err != nil {
		return Artifact{}, fmt.Errorf("invalid destination: %w", err)
	}
	if folder != "" {
		destRel = filepath.Join(folder, destRel)
	}
	if isReservedArtifactPath(destRel) {
		return Artifact{}, fmt.Errorf("destination %q is reserved", destRel)
	}

	manifest, err := LoadManifest(sess)
	if err != nil {
		return Artifact{}, err
	}
	if existing, _ := Find(manifest, id); existing != nil {
		return Artifact{}, fmt.Errorf("artifact id %q already exists", id)
	}

	if err := os.MkdirAll(DirForSession(sess), 0o755); err != nil {
		return Artifact{}, err
	}
	if err := EnsureTheme(sess); err != nil {
		return Artifact{}, fmt.Errorf("failed to write default artifact theme: %w", err)
	}

	finalRel, finalAbs, err := uniqueDestination(sess, destRel, opts.ID != "")
	if err != nil {
		return Artifact{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
		return Artifact{}, err
	}
	if err := copyTo(finalAbs, opts); err != nil {
		_ = os.Remove(finalAbs)
		return Artifact{}, err
	}

	var summary *string
	if opts.Summary != "" {
		s := opts.Summary
		summary = &s
	}
	artifact := Artifact{
		ID:        id,
		Type:      artifactType,
		Title:     opts.Title,
		File:      filepath.ToSlash(finalRel),
		Folder:    folder,
		Created:   now.UTC(),
		Agent:     agent,
		Retention: retention,
		Summary:   summary,
		Tags:      opts.Tags,
		Assets:    DiscoverAssetBundle(sess, finalRel),
		Focus:     opts.Focus,
	}
	if err := ValidateArtifact(artifact); err != nil {
		return Artifact{}, err
	}

	if opts.Focus {
		for i := range manifest.Artifacts {
			manifest.Artifacts[i].Focus = false
		}
	}
	if _, idx := Find(manifest, id); idx >= 0 {
		manifest.Artifacts[idx] = artifact
	} else {
		manifest.Artifacts = append(manifest.Artifacts, artifact)
	}
	if err := SaveManifest(sess, manifest); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

func isReservedArtifactPath(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	return clean == ManifestName || clean == "theme.css" || strings.HasPrefix(filepath.Base(clean), ".manifest")
}

func uniqueDestination(sess *session.Session, rel string, allowOverwrite bool) (string, string, error) {
	abs, err := SecureNewPath(DirForSession(sess), rel)
	if err != nil {
		return "", "", err
	}
	if allowOverwrite {
		if _, err := os.Lstat(abs); err == nil {
			return "", "", fmt.Errorf("destination %q already exists", rel)
		} else if err != nil && !os.IsNotExist(err) {
			return "", "", err
		}
		return filepath.ToSlash(filepath.Clean(rel)), abs, nil
	}
	if _, err := os.Lstat(abs); os.IsNotExist(err) {
		return filepath.ToSlash(filepath.Clean(rel)), abs, nil
	} else if err != nil {
		return "", "", err
	}
	ext := filepath.Ext(rel)
	stem := strings.TrimSuffix(rel, ext)
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		candidateAbs, err := SecureNewPath(DirForSession(sess), candidate)
		if err != nil {
			return "", "", err
		}
		if _, err := os.Lstat(candidateAbs); os.IsNotExist(err) {
			return filepath.ToSlash(filepath.Clean(candidate)), candidateAbs, nil
		} else if err != nil {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("could not find unique artifact destination for %q", rel)
}

func copyTo(destAbs string, opts AddOptions) error {
	out, err := os.OpenFile(destAbs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create artifact file: %w", err)
	}
	defer out.Close()
	if opts.Reader != nil {
		if _, err := io.Copy(out, opts.Reader); err != nil {
			return fmt.Errorf("failed to write artifact file: %w", err)
		}
		return nil
	}
	if opts.Source == "" || opts.Source == "-" {
		return fmt.Errorf("source file is required")
	}
	in, err := os.Open(opts.Source)
	if err != nil {
		return fmt.Errorf("failed to open source artifact: %w", err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source artifact: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source artifact must be a file")
	}
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("failed to copy artifact file: %w", err)
	}
	return nil
}
