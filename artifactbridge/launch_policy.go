package artifactbridge

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LaunchPolicy is the owner's managed-agent launch policy: which projects
// pi_start_task may start agents in, and how Pi is launched. It comes only
// from the owner's DevX config file (see ReadOwnerConfig), never from a
// project .devx/config.yaml or DEVX_* environment variables: a repository
// must not be able to widen the start allowlist or choose the executable,
// arguments or environment of a managed agent.
//
//	pi_mcp:
//	  allowed_projects: [...]   # shared with the artifact bridge
//	  pi_command: pi            # optional
//	  pi_args: [...]            # optional
//	  pass_env: [NAME, ...]     # optional
type LaunchPolicy struct {
	AllowedProjects []string
	PiCommand       string
	PiArgs          []string
	PassEnv         []string
}

// LoadLaunchPolicyFile reads the launch policy from the owner's config file.
// A missing file yields the zero policy (no allowed projects, so every start
// is denied). Any read, trust, parse or validation problem is an error.
func LoadLaunchPolicyFile(path string) (LaunchPolicy, error) {
	data, err := ReadOwnerConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return LaunchPolicy{}, nil
	}
	if err != nil {
		return LaunchPolicy{}, err
	}
	return ParseLaunchPolicy(data)
}

// ParseLaunchPolicy extracts and strictly validates pi_mcp.allowed_projects,
// pi_command, pi_args and pass_env. Other keys (including pi_mcp.artifacts)
// are ignored here. Wrong types, nulls, empty names, duplicate keys, aliases
// and merge keys on the paths read are errors.
func ParseLaunchPolicy(data []byte) (LaunchPolicy, error) {
	var lp LaunchPolicy
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return lp, fmt.Errorf("parse config: %w", err)
	}
	if doc.Kind == 0 { // empty document
		return lp, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return lp, fmt.Errorf("config root must be a mapping")
	}
	piMCP, err := lookup(doc.Content[0], "pi_mcp")
	if err != nil || piMCP == nil {
		return lp, err
	}
	if piMCP.Kind != yaml.MappingNode {
		return lp, fmt.Errorf("pi_mcp must be a mapping")
	}
	lists := []struct {
		key string
		dst *[]string
	}{
		{"allowed_projects", &lp.AllowedProjects},
		{"pi_args", &lp.PiArgs},
		{"pass_env", &lp.PassEnv},
	}
	for _, l := range lists {
		n, err := lookup(piMCP, l.key)
		if err != nil {
			return LaunchPolicy{}, err
		}
		if n == nil {
			continue
		}
		if *l.dst, err = stringList(n, "pi_mcp."+l.key); err != nil {
			return LaunchPolicy{}, err
		}
	}
	n, err := lookup(piMCP, "pi_command")
	if err != nil {
		return LaunchPolicy{}, err
	}
	if n != nil {
		if n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value == "" {
			return LaunchPolicy{}, fmt.Errorf("pi_mcp.pi_command must be a non-empty string")
		}
		lp.PiCommand = n.Value
	}
	return lp, nil
}
