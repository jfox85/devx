package artifactbridge

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Policy is the owner's artifact-bridge policy. It is re-read and validated
// on every tools/list and tool call (see Service.policy), so edits to the
// owner config take effect on the next call without restarting `devx mcp pi`
// or the relay. A config that cannot be read or does not validate disables the
// bridge for that call (fail closed).
//
//	pi_mcp:
//	  allowed_projects: [...]      # shared with pi_start_task
//	  artifacts:
//	    read: true                 # default true
//	    upload: false              # default false
//	    sessions: [a, b]           # optional restrictive allowlist (present, even empty = only these)
//	    exclude_sessions: [c]      # optional, always subtracted
//	    exclude_projects: [p]      # optional, always subtracted
//	    max_upload_bytes: 10485760 # optional
//
// Read eligibility, when `sessions` is absent, is every MCP-visible managed
// session (a live managed agent whose DevX session record still belongs to it)
// in an allowed project, minus exclusions. Upload eligibility never uses that
// default: it requires upload: true AND an explicit `sessions` list naming the
// session.
type Policy struct {
	Read            bool
	Upload          bool
	AllowedProjects []string
	// Sessions is the explicit allowlist; SessionsSet reports whether the
	// key was present (an explicit empty list exposes nothing).
	Sessions        []string
	SessionsSet     bool
	ExcludeSessions []string
	ExcludeProjects []string
	MaxUploadBytes  int64
}

// DefaultPolicy applies when the config file has no pi_mcp.artifacts block.
// With no allowed projects nothing is eligible.
func DefaultPolicy() Policy {
	return Policy{Read: true, MaxUploadBytes: DefaultMaxUploadBytes}
}

const maxPolicyFileBytes = 1 << 20

var errPolicy = errf(codeUnavailable, "artifact bridge configuration is unreadable or invalid; nothing is served until it is fixed")

// LoadPolicyFile reads the policy from one DevX config file. A missing file
// yields DefaultPolicy (which has no allowed projects, so nothing is
// eligible). Any read, parse or validation problem is an error.
func LoadPolicyFile(path string) (Policy, error) {
	p := DefaultPolicy()
	if path == "" {
		return p, fmt.Errorf("no policy file configured")
	}
	// Open once and check the opened file (not the path), then read through a
	// limit, so a file swapped or grown after the check cannot be read
	// unbounded. O_NONBLOCK keeps a FIFO from blocking the open.
	f, err := openPolicy(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return p, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPolicyFileBytes {
		return p, fmt.Errorf("config is not a regular file of at most %d bytes", maxPolicyFileBytes)
	}
	if err := policyFileTrusted(info); err != nil {
		return p, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPolicyFileBytes+1))
	if err != nil {
		return p, err
	}
	if len(data) > maxPolicyFileBytes {
		return p, fmt.Errorf("config is not a regular file of at most %d bytes", maxPolicyFileBytes)
	}
	return ParsePolicy(data)
}

// ParsePolicy extracts and strictly validates pi_mcp.allowed_projects and
// pi_mcp.artifacts from a DevX config document. Other keys are ignored.
// Within pi_mcp.artifacts, unknown keys, null values, wrong types, aliases and
// duplicate keys are errors: a typo such as `session:` must never fall back to
// the default-wide scope.
func ParsePolicy(data []byte) (Policy, error) {
	p := DefaultPolicy()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return p, fmt.Errorf("parse config: %w", err)
	}
	if doc.Kind == 0 { // empty document
		return p, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return p, fmt.Errorf("config root must be a mapping")
	}
	piMCP, err := lookup(doc.Content[0], "pi_mcp")
	if err != nil || piMCP == nil {
		return p, err
	}
	if piMCP.Kind != yaml.MappingNode {
		return p, fmt.Errorf("pi_mcp must be a mapping")
	}
	if n, err := lookup(piMCP, "allowed_projects"); err != nil {
		return p, err
	} else if n != nil {
		if p.AllowedProjects, err = stringList(n, "pi_mcp.allowed_projects"); err != nil {
			return p, err
		}
	}
	art, err := lookup(piMCP, "artifacts")
	if err != nil || art == nil {
		return p, err
	}
	if art.Kind != yaml.MappingNode {
		return p, fmt.Errorf("pi_mcp.artifacts must be a mapping")
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(art.Content); i += 2 {
		k, v := art.Content[i], art.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return p, fmt.Errorf("pi_mcp.artifacts has a non-scalar key")
		}
		key := k.Value
		if seen[key] {
			return p, fmt.Errorf("pi_mcp.artifacts.%s is set twice", key)
		}
		seen[key] = true
		name := "pi_mcp.artifacts." + key
		switch key {
		case "read":
			p.Read, err = boolean(v, name)
		case "upload":
			p.Upload, err = boolean(v, name)
		case "sessions":
			p.Sessions, err = stringList(v, name)
			p.SessionsSet = true
		case "exclude_sessions":
			p.ExcludeSessions, err = stringList(v, name)
		case "exclude_projects":
			p.ExcludeProjects, err = stringList(v, name)
		case "max_upload_bytes":
			var n int64
			if v.Kind != yaml.ScalarNode || v.Tag != "!!int" || v.Decode(&n) != nil || n <= 0 {
				return p, fmt.Errorf("%s must be a positive integer", name)
			}
			if n > MaxUploadBytesCeiling {
				n = MaxUploadBytesCeiling
			}
			p.MaxUploadBytes = n
		default:
			return p, fmt.Errorf("unknown key %s", name)
		}
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

// lookup returns the value node for key in a mapping, rejecting duplicate
// keys and YAML aliases/merge keys anywhere on the path we read.
func lookup(m *yaml.Node, key string) (*yaml.Node, error) {
	var found *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == "<<" {
			return nil, fmt.Errorf("YAML merge keys are not supported in the DevX config")
		}
		if k.Kind == yaml.ScalarNode && k.Value == key {
			if found != nil {
				return nil, fmt.Errorf("%s is set twice", key)
			}
			if v.Kind == yaml.AliasNode {
				return nil, fmt.Errorf("%s must not be a YAML alias", key)
			}
			found = v
		}
	}
	return found, nil
}

func boolean(v *yaml.Node, name string) (bool, error) {
	var b bool
	if v.Kind != yaml.ScalarNode || v.Tag != "!!bool" || v.Decode(&b) != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return b, nil
}

func stringList(v *yaml.Node, name string) ([]string, error) {
	if v.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s must be a list", name)
	}
	out := make([]string, 0, len(v.Content))
	for _, it := range v.Content {
		if it.Kind != yaml.ScalarNode || it.Tag != "!!str" || it.Value == "" {
			return nil, fmt.Errorf("%s must contain only non-empty names", name)
		}
		out = append(out, it.Value)
	}
	return out, nil
}

// readEligible reports whether a session/project passes the read policy.
func (p Policy) readEligible(sessionName, project string) bool {
	if !p.Read || !contains(p.AllowedProjects, project) {
		return false
	}
	if contains(p.ExcludeSessions, sessionName) || contains(p.ExcludeProjects, project) {
		return false
	}
	return !p.SessionsSet || contains(p.Sessions, sessionName)
}

// uploadEligible requires upload: true and an explicit sessions list naming
// the session (never the default-wide read scope), plus the read-side
// project and exclusion checks.
func (p Policy) uploadEligible(sessionName, project string) bool {
	if !p.Upload || !p.SessionsSet || !contains(p.Sessions, sessionName) || !contains(p.AllowedProjects, project) {
		return false
	}
	return !contains(p.ExcludeSessions, sessionName) && !contains(p.ExcludeProjects, project)
}
