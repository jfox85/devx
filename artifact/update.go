package artifact

import (
	"fmt"
	"os"

	"github.com/jfox85/devx/session"
)

func Remove(sess *session.Session, id string) (out Artifact, err error) {
	err = withManifestLock(sess, func() error {
		manifest, err := LoadManifest(sess)
		if err != nil {
			return err
		}
		a, idx := Find(manifest, id)
		if a == nil {
			return fmt.Errorf("artifact %q not found", id)
		}
		removed := *a
		abs, err := SecureExistingPath(DirForSession(sess), removed.File)
		if err != nil {
			return err
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove artifact file: %w", err)
		}
		manifest.Artifacts = append(manifest.Artifacts[:idx], manifest.Artifacts[idx+1:]...)
		removeUnreferencedAssets(sess, manifest, removed.Assets)
		if err := SaveManifest(sess, manifest); err != nil {
			return err
		}
		out = removed
		return nil
	})
	return out, err
}

func removeUnreferencedAssets(sess *session.Session, manifest *Manifest, assets []string) {
	if len(assets) == 0 {
		return
	}
	stillReferenced := map[string]bool{}
	for _, a := range manifest.Artifacts {
		stillReferenced[a.File] = true
		for _, asset := range a.Assets {
			stillReferenced[asset] = true
		}
	}
	for _, asset := range assets {
		if stillReferenced[asset] {
			continue
		}
		abs, err := SecureExistingPath(DirForSession(sess), asset)
		if err != nil {
			continue
		}
		_ = os.Remove(abs)
	}
}

func SetRetention(sess *session.Session, id, retention string) (out Artifact, err error) {
	if retention == "" {
		retention = DefaultRetention
	}
	if err := ValidateRetention(retention); err != nil {
		return Artifact{}, err
	}
	err = withManifestLock(sess, func() error {
		manifest, err := LoadManifest(sess)
		if err != nil {
			return err
		}
		a, _ := Find(manifest, id)
		if a == nil {
			return fmt.Errorf("artifact %q not found", id)
		}
		a.Retention = retention
		out = *a
		return SaveManifest(sess, manifest)
	})
	return out, err
}
