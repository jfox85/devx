package artifact

import (
	"github.com/jfox85/devx/session"
)

func ClearFocus(sess *session.Session) error {
	return withManifestLock(sess, func() error {
		manifest, err := LoadManifest(sess)
		if err != nil {
			return err
		}
		changed := false
		for i := range manifest.Artifacts {
			if manifest.Artifacts[i].Focus {
				manifest.Artifacts[i].Focus = false
				changed = true
			}
		}
		if !changed {
			return nil
		}
		return SaveManifest(sess, manifest)
	})
}

func FocusedID(sess *session.Session) string {
	manifest, err := LoadManifest(sess)
	if err != nil {
		return ""
	}
	for _, a := range manifest.Artifacts {
		if a.Focus {
			return a.ID
		}
	}
	return ""
}
