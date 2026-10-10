package artifact

import (
	"errors"
	"fmt"
	"os"

	"github.com/jfox85/devx/session"
)

// ErrIDExists reports that a manifest entry with the requested ID exists.
var ErrIDExists = errors.New("artifact id already exists")

// RegisterExisting records a file that the caller has already written under
// the session's artifact directory. It never writes or moves file content and
// records no referenced assets (callers use it for untrusted content, whose
// references must not make removal delete other files). It fails if the ID is
// taken, if another entry already names the same file, or if the file is not
// a regular file reachable without symlinks.
func RegisterExisting(sess *session.Session, a Artifact) (out Artifact, err error) {
	a.Assets = nil
	if a.Retention == "" {
		a.Retention = DefaultRetention
	}
	if err := ValidateArtifact(a); err != nil {
		return Artifact{}, err
	}
	err = withManifestLock(sess, func() error {
		m, err := LoadManifest(sess)
		if err != nil {
			return err
		}
		if existing, _ := Find(m, a.ID); existing != nil {
			return fmt.Errorf("%w: %q", ErrIDExists, a.ID)
		}
		for _, x := range m.Artifacts {
			if x.File == a.File {
				return fmt.Errorf("file %q is already registered", a.File)
			}
		}
		abs, err := SecureExistingPath(DirForSession(sess), a.File)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(abs); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("artifact file is not a regular file")
		}
		m.Artifacts = append(m.Artifacts, a)
		if err := SaveManifest(sess, m); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}
