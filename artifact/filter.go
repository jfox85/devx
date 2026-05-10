package artifact

import "strings"

type FilterOptions struct {
	Type   string
	Tag    string
	Agent  string
	Search string
	Folder string
}

func Filter(artifacts []Artifact, opts FilterOptions) []Artifact {
	search := strings.ToLower(strings.TrimSpace(opts.Search))
	folder := strings.TrimSpace(opts.Folder)
	if folder != "" {
		if normalized, err := NormalizeFolderPath(folder); err == nil {
			folder = normalized
		}
	}
	out := make([]Artifact, 0, len(artifacts))
	for _, a := range artifacts {
		if opts.Type != "" && a.Type != opts.Type {
			continue
		}
		if opts.Agent != "" && a.Agent != opts.Agent {
			continue
		}
		if folder != "" && a.Folder != folder {
			continue
		}
		if opts.Tag != "" && !hasTag(a, opts.Tag) {
			continue
		}
		if search != "" && !matchesSearch(a, search) {
			continue
		}
		out = append(out, a)
	}
	SortNewestFirst(out)
	return out
}

func hasTag(a Artifact, tag string) bool {
	for _, t := range a.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func matchesSearch(a Artifact, search string) bool {
	fields := []string{a.ID, a.Type, a.Title, a.File, a.Folder, a.Agent, a.Retention}
	if a.Summary != nil {
		fields = append(fields, *a.Summary)
	}
	fields = append(fields, a.Tags...)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), search) {
			return true
		}
	}
	return false
}
