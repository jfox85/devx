package artifact

import (
	"fmt"
	"strings"

	"github.com/jfox85/devx/session"
)

type MetadataUpdate struct {
	Title     *string
	Type      *string
	Summary   *string
	Tags      []string
	TagsSet   bool
	Retention *string
	Focus     *bool
}

func UpdateMetadata(sess *session.Session, id string, update MetadataUpdate) (out Artifact, err error) {
	err = withManifestLock(sess, func() error {
		manifest, err := LoadManifest(sess)
		if err != nil {
			return fmt.Errorf("loading manifest: %w", err)
		}
		a, _ := Find(manifest, id)
		if a == nil {
			return fmt.Errorf("artifact %q not found", id)
		}
		if update.Title != nil {
			if strings.TrimSpace(*update.Title) == "" {
				return fmt.Errorf("artifact title is required")
			}
			a.Title = *update.Title
		}
		if update.Type != nil {
			if err := ValidateType(*update.Type); err != nil {
				return fmt.Errorf("validating type %q: %w", *update.Type, err)
			}
			a.Type = *update.Type
		}
		if update.Summary != nil {
			if *update.Summary == "" {
				a.Summary = nil
			} else {
				s := *update.Summary
				a.Summary = &s
			}
		}
		if update.TagsSet {
			a.Tags = update.Tags
		}
		if update.Retention != nil {
			if err := ValidateRetention(*update.Retention); err != nil {
				return fmt.Errorf("validating retention %q: %w", *update.Retention, err)
			}
			a.Retention = *update.Retention
		}
		if update.Focus != nil {
			if *update.Focus {
				for i := range manifest.Artifacts {
					manifest.Artifacts[i].Focus = false
				}
			}
			a.Focus = *update.Focus
		}
		out = *a
		return SaveManifest(sess, manifest)
	})
	return out, err
}
