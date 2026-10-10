package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrIDExists reports that a manifest entry with the requested ID exists.
var ErrIDExists = errors.New("artifact id already exists")

// LockFileName is the manifest lock file inside the artifact directory. Every
// writer takes an exclusive flock on it before a read-modify-write of the
// manifest (see withManifestLock).
const LockFileName = ".manifest.lock"

// AppendRegistered validates a and appends it to m, for a file the caller has
// already written under the artifact directory. It performs no I/O, so callers
// that must avoid path-based (symlink-following) access can do the file system
// work themselves. It records no referenced assets (callers use it for
// untrusted content, whose references must not make removal delete other
// files). It fails if the ID is taken or another entry names the same file.
func AppendRegistered(m *Manifest, a Artifact) (Artifact, error) {
	if m == nil {
		return Artifact{}, fmt.Errorf("manifest is nil")
	}
	a.Assets = nil
	if a.Retention == "" {
		a.Retention = DefaultRetention
	}
	if err := ValidateArtifact(a); err != nil {
		return Artifact{}, err
	}
	if existing, _ := Find(m, a.ID); existing != nil {
		return Artifact{}, fmt.Errorf("%w: %q", ErrIDExists, a.ID)
	}
	for _, x := range m.Artifacts {
		if x.File == a.File {
			return Artifact{}, fmt.Errorf("file %q is already registered", a.File)
		}
	}
	m.Artifacts = append(m.Artifacts, a)
	return a, nil
}

// EncodeManifest validates m for sessionName and returns the exact bytes
// SaveManifest writes.
func EncodeManifest(m *Manifest, sessionName string) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}
	m.Version = ManifestVersion
	m.Session = sessionName
	if m.Artifacts == nil {
		m.Artifacts = []Artifact{}
	}
	if err := ValidateManifest(m); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal artifact manifest: %w", err)
	}
	return append(data, '\n'), nil
}
