package session

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ServiceConfig represents a service definition from config.yaml
type ServiceConfig struct {
	Name     string            // derived from map key
	Command  string            `yaml:"command"`
	Cwd      string            `yaml:"cwd"`
	Port     int               `yaml:"port"` // 0 means auto
	PortAuto bool              // true if port was "auto" or not specified
	Env      map[string]string `yaml:"env"`
}

// serviceConfigRaw is used for initial parsing before port handling
type serviceConfigRaw struct {
	Command string            `yaml:"command"`
	Cwd     string            `yaml:"cwd"`
	Port    interface{}       `yaml:"port"` // can be int or "auto"
	Env     map[string]string `yaml:"env"`
}

type configFile struct {
	Services map[string]serviceConfigRaw `yaml:"services"`
}

// ParseServiceConfigs parses service configurations from YAML
func ParseServiceConfigs(data []byte) (map[string]*ServiceConfig, error) {
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	result := make(map[string]*ServiceConfig)
	for name, raw := range cfg.Services {
		sc := &ServiceConfig{
			Name:    name,
			Command: raw.Command,
			Cwd:     raw.Cwd,
			Env:     raw.Env,
		}

		// Handle port (can be int, "auto", or nil)
		switch v := raw.Port.(type) {
		case int:
			sc.Port = v
			sc.PortAuto = false
		case float64:
			sc.Port = int(v)
			sc.PortAuto = false
		case string:
			if v == "auto" {
				sc.PortAuto = true
			} else {
				return nil, fmt.Errorf("service %s: invalid port value '%s'", name, v)
			}
		case nil:
			sc.PortAuto = true // default to auto
		default:
			return nil, fmt.Errorf("service %s: invalid port type", name)
		}

		result[name] = sc
	}

	return result, nil
}
