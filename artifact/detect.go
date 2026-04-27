package artifact

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func DetectType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "screenshot"
	case ".webm", ".mp4", ".mov":
		return "recording"
	case ".log", ".txt":
		return "log"
	case ".diff", ".patch":
		return "diff"
	case ".html", ".htm", ".md", ".pdf":
		return "document"
	default:
		return "other"
	}
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "artifact"
	}
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
		if s == "" {
			return "artifact"
		}
	}
	return s
}

func GenerateID(artifactType, title string, t time.Time) string {
	utc := t.UTC()
	return artifactType + "-" + Slugify(title) + "-" + utc.Format("20060102150405") + "-" + fmt.Sprintf("%09d", utc.Nanosecond())
}

func DefaultDestination(artifactType, sourceName string) string {
	base := filepath.Base(sourceName)
	if base == "." || base == string(filepath.Separator) || base == "" || base == "-" {
		base = "artifact"
	}
	switch artifactType {
	case "screenshot":
		return filepath.Join("screenshots", base)
	case "recording":
		return filepath.Join("recordings", base)
	case "log":
		return filepath.Join("logs", base)
	default:
		return base
	}
}

func ParseTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	seen := map[string]bool{}
	var tags []string
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	return tags
}

func WebPath(sessionName, file string) string {
	segments := strings.Split(filepath.ToSlash(file), "/")
	escaped := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(segment))
	}
	return "/sessions/" + url.PathEscape(sessionName) + "/artifacts/" + strings.Join(escaped, "/")
}
