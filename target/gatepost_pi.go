package target

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type gatepostProviderBootstrap struct {
	Env        map[string]string
	Mode       string
	Command    string
	Registered []string
	Warnings   []string
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func piConfigDir() string {
	if v := os.Getenv("PI_CONFIG_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "projects", "pi-config")
}

func piModelsFile() string {
	if v := os.Getenv("DEVX_GATEPOST_PI_MODELS_FILE"); v != "" {
		return v
	}
	dir := piConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "models.container.json")
}

func bootstrapGatepostProviderSecrets(cfg GatepostRuntimeConfig, gatepostRoot, controlURL, token string) (gatepostProviderBootstrap, error) {
	result := gatepostProviderBootstrap{Env: map[string]string{}}
	cmdPath, args, mode, err := gatepostProviderBootstrapCommand(cfg, gatepostRoot, controlURL)
	if err == nil {
		result.Mode = mode
		result.Command = strings.Join(append([]string{cmdPath}, args...), " ")
		cmd := exec.Command(cmdPath, args...)
		cmd.Env = setEnv(os.Environ(), "GATEPOST_CONTROL_TOKEN", token)
		if cfg.AuthHome != "" {
			cmd.Env = setEnv(cmd.Env, "HOME", cfg.AuthHome)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if runErr := cmd.Run(); runErr == nil {
			parseProviderBootstrapOutput(stdout.String(), &result)
			if err := requireGatepostProviders(cfg, result.Registered); err != nil {
				return result, err
			}
			return result, nil
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s failed with exit status; stderr suppressed", result.Mode))
			_ = stderr.String()
		}
	}

	registered, fallbackErr := registerGatepostSecrets(controlURL, token)
	if fallbackErr != nil {
		if err != nil {
			return result, err
		}
		return result, fmt.Errorf("gatepost env provider bootstrap failed: %w", fallbackErr)
	}
	result.Mode = "host-env"
	result.Command = "host environment"
	result.Registered = registered
	if len(registered) > 0 && len(result.Warnings) > 0 {
		result.Warnings = append(result.Warnings, "continued with host environment provider credentials")
	}
	if err := requireGatepostProviders(cfg, result.Registered); err != nil {
		return result, err
	}
	return result, nil
}

func gatepostProviderBootstrapCommand(cfg GatepostRuntimeConfig, gatepostRoot, controlURL string) (string, []string, string, error) {
	if raw := cfg.ProviderBootstrapCommand; raw != "" {
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			return "", nil, "", fmt.Errorf("gatepost.provider_bootstrap_command is empty")
		}
		return fields[0], append(fields[1:], controlURL), "command", nil
	}
	if gatepostRoot != "" {
		script := filepath.Join(gatepostRoot, "scripts", "make-pi-tokens.py")
		if _, err := os.Stat(script); err == nil {
			return "python3", []string{script, controlURL}, "gatepost-helper", nil
		}
	}
	return "", nil, "", fmt.Errorf("no Gatepost provider bootstrap command configured")
}

func parseProviderBootstrapOutput(output string, result *gatepostProviderBootstrap) {
	registered := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k != "" {
			switch k {
			case "CODEX_FAKE_JWT", "CLIPROXYAPI_API_KEY_PLACEHOLDER":
				result.Env[k] = v
			case "GATEPOST_SECRET_ANTHROPIC_REGISTERED":
				registered["anthropic-oauth"] = true
			case "GATEPOST_SECRET_CODEX_REGISTERED":
				registered["codex-oauth"] = true
			case "GATEPOST_SECRET_OPENAI_REGISTERED":
				registered["openai-key"] = true
			case "GATEPOST_SECRET_CLIPROXY_REGISTERED":
				registered["cliproxy-key"] = true
			case "GATEPOST_SECRET_GEMINI_REGISTERED":
				registered["gemini-key"] = true
			case "GATEPOST_SECRET_GH_REGISTERED":
				registered["gh-token"] = true
			}
		}
	}
	if result.Env["CLIPROXYAPI_API_KEY_PLACEHOLDER"] != "" {
		result.Env["CLIPROXYAPI_API_KEY"] = result.Env["CLIPROXYAPI_API_KEY_PLACEHOLDER"]
		delete(result.Env, "CLIPROXYAPI_API_KEY_PLACEHOLDER")
	}
	for name := range registered {
		result.Registered = append(result.Registered, name)
	}
	sort.Strings(result.Registered)
}

func requireGatepostProviders(cfg GatepostRuntimeConfig, registered []string) error {
	requiredRaw := getenvDefault("DEVX_GATEPOST_REQUIRED_PROVIDERS", cfg.RequiredProviders)
	if requiredRaw == "" {
		if len(registered) == 0 {
			return fmt.Errorf("no Gatepost providers were registered; set OPENAI_API_KEY, GEMINI_API_KEY, CLIPROXYAPI_API_KEY, or configure Pi/Gatepost provider bootstrap")
		}
		return nil
	}
	registeredSet := map[string]bool{}
	for _, name := range registered {
		registeredSet[name] = true
	}
	missing := []string{}
	for _, item := range strings.Split(requiredRaw, ",") {
		name := strings.TrimSpace(item)
		if name != "" && !registeredSet[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required Gatepost providers missing: %s", strings.Join(missing, ", "))
	}
	return nil
}
