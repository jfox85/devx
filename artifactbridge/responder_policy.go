package artifactbridge

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ResponderPolicy is the owner's `devx ask` responder policy: whether a
// local command answers asks, when, and which executable and arguments it
// runs. It comes only from the owner's DevX config file (see
// ReadOwnerConfig), never from a project .devx/config.yaml or DEVX_*
// environment variables: a repository must not be able to enable the
// responder, switch it to run without approval, or choose what it executes.
//
//	agent_responder:
//	  enabled: true
//	  mode: approval   # none | approval | always
//	  command: pi
//	  args: [...]
//	  timeout: 5m
//	  read_only: true
type ResponderPolicy struct {
	Enabled  bool
	Mode     string
	Command  string
	Args     []string
	Timeout  time.Duration
	ReadOnly bool
}

// DefaultResponderPolicy is used for keys the owner config leaves unset.
func DefaultResponderPolicy() ResponderPolicy {
	return ResponderPolicy{
		Enabled:  false,
		Mode:     "approval",
		Command:  "pi",
		Args:     []string{"--print", "--no-session", "@{{.PromptFile}}"},
		Timeout:  5 * time.Minute,
		ReadOnly: true,
	}
}

// LoadResponderPolicyFile reads the responder policy from the owner's config
// file. A missing file yields the defaults (responder disabled). Any read,
// trust, parse or validation problem is an error; callers fail closed.
func LoadResponderPolicyFile(path string) (ResponderPolicy, error) {
	data, err := ReadOwnerConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultResponderPolicy(), nil
	}
	if err != nil {
		return ResponderPolicy{}, err
	}
	return ParseResponderPolicy(data)
}

// ParseResponderPolicy strictly validates agent_responder. Unset keys take
// DefaultResponderPolicy values. Wrong types, nulls, unknown keys, duplicate
// keys, aliases and merge keys are errors.
func ParseResponderPolicy(data []byte) (ResponderPolicy, error) {
	rp := DefaultResponderPolicy()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ResponderPolicy{}, fmt.Errorf("parse config: %w", err)
	}
	if doc.Kind == 0 {
		return rp, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return ResponderPolicy{}, fmt.Errorf("config root must be a mapping")
	}
	node, err := lookup(doc.Content[0], "agent_responder")
	if err != nil || node == nil {
		return rp, err
	}
	if node.Kind != yaml.MappingNode {
		return ResponderPolicy{}, fmt.Errorf("agent_responder must be a mapping")
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return ResponderPolicy{}, fmt.Errorf("agent_responder has a non-scalar key")
		}
		if k.Value == "<<" {
			return ResponderPolicy{}, fmt.Errorf("YAML merge keys are not supported in the DevX config")
		}
		if v.Kind == yaml.AliasNode {
			return ResponderPolicy{}, fmt.Errorf("agent_responder.%s must not be a YAML alias", k.Value)
		}
		if seen[k.Value] {
			return ResponderPolicy{}, fmt.Errorf("agent_responder.%s is set twice", k.Value)
		}
		seen[k.Value] = true
		name := "agent_responder." + k.Value
		switch k.Value {
		case "enabled":
			rp.Enabled, err = boolean(v, name)
		case "read_only":
			rp.ReadOnly, err = boolean(v, name)
		case "mode":
			rp.Mode, err = nonEmptyString(v, name)
			if err == nil && rp.Mode != "none" && rp.Mode != "approval" && rp.Mode != "always" {
				err = fmt.Errorf("%s must be none, approval or always", name)
			}
		case "command":
			rp.Command, err = nonEmptyString(v, name)
		case "args":
			if v.Kind != yaml.SequenceNode {
				return ResponderPolicy{}, fmt.Errorf("%s must be a list", name)
			}
			rp.Args = make([]string, 0, len(v.Content))
			for _, it := range v.Content {
				if it.Kind != yaml.ScalarNode || it.Tag != "!!str" {
					return ResponderPolicy{}, fmt.Errorf("%s must contain only strings", name)
				}
				rp.Args = append(rp.Args, it.Value)
			}
		case "timeout":
			var s string
			if s, err = nonEmptyString(v, name); err == nil {
				if rp.Timeout, err = time.ParseDuration(s); err != nil || rp.Timeout <= 0 {
					err = fmt.Errorf("%s must be a positive duration such as 5m", name)
				}
			}
		default:
			return ResponderPolicy{}, fmt.Errorf("unknown key %s", name)
		}
		if err != nil {
			return ResponderPolicy{}, err
		}
	}
	return rp, nil
}

func nonEmptyString(v *yaml.Node, name string) (string, error) {
	if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Value == "" {
		return "", fmt.Errorf("%s must be a non-empty string", name)
	}
	return v.Value, nil
}
