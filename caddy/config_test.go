package caddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestBuildCaddyConfig(t *testing.T) {
	// Ensure clean Viper state for all subtests
	viper.Set("caddy_admin", "")

	t.Run("empty sessions produces valid config with no routes", func(t *testing.T) {
		sessions := map[string]*SessionInfo{}
		config := BuildCaddyConfig(sessions)

		jsonData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		jsonStr := string(jsonData)
		// Should have admin listener
		if !contains(jsonStr, `"listen":"localhost:2019"`) {
			t.Errorf("missing admin listener in config: %s", jsonStr)
		}
		// Should have server listening on :80
		if !contains(jsonStr, `":80"`) {
			t.Errorf("missing :80 listener in config: %s", jsonStr)
		}
		// Routes should be empty array, not null
		if !contains(jsonStr, `"routes":[]`) {
			t.Errorf("expected empty routes array in config: %s", jsonStr)
		}
	})

	t.Run("single session produces correct routes", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"my-session": {
				Name:  "my-session",
				Ports: map[string]int{"FRONTEND": 3000, "BACKEND": 4000},
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		jsonStr := string(jsonData)
		if !contains(jsonStr, `my-session-frontend.localhost`) {
			t.Errorf("missing frontend hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `my-session-backend.localhost`) {
			t.Errorf("missing backend hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `localhost:3000`) {
			t.Errorf("missing frontend port: %s", jsonStr)
		}
		if !contains(jsonStr, `localhost:4000`) {
			t.Errorf("missing backend port: %s", jsonStr)
		}
	})

	t.Run("session with project alias includes prefix", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"my-session": {
				Name:         "my-session",
				Ports:        map[string]int{"FRONTEND": 3000},
				ProjectAlias: "myproject",
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		jsonStr := string(jsonData)
		if !contains(jsonStr, `myproject-my-session-frontend.localhost`) {
			t.Errorf("missing project-prefixed hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `sess-myproject-my-session-frontend`) {
			t.Errorf("missing project-prefixed route ID: %s", jsonStr)
		}
	})

	t.Run("route IDs and hostnames are deterministically ordered", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"b-session": {
				Name:  "b-session",
				Ports: map[string]int{"UI": 3000},
			},
			"a-session": {
				Name:  "a-session",
				Ports: map[string]int{"UI": 4000},
			},
		}
		config1 := BuildCaddyConfig(sessions)
		config2 := BuildCaddyConfig(sessions)

		json1, _ := json.Marshal(config1)
		json2, _ := json.Marshal(config2)

		if string(json1) != string(json2) {
			t.Errorf("config generation is not deterministic")
		}
	})

	t.Run("session with slashes in name is sanitized", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"feature/my-branch": {
				Name:  "feature/my-branch",
				Ports: map[string]int{"FRONTEND": 3000},
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, _ := json.Marshal(config)
		jsonStr := string(jsonData)
		// Slashes should be converted to hyphens
		if !contains(jsonStr, `feature-my-branch-frontend.localhost`) {
			t.Errorf("session name with slash not properly sanitized: %s", jsonStr)
		}
	})

	t.Run("session without project alias when others have one", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"with-project": {
				Name:         "with-project",
				Ports:        map[string]int{"UI": 3000},
				ProjectAlias: "myapp",
			},
			"no-project": {
				Name:  "no-project",
				Ports: map[string]int{"UI": 4000},
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, _ := json.Marshal(config)
		jsonStr := string(jsonData)
		// Project-prefixed session
		if !contains(jsonStr, `myapp-with-project-ui.localhost`) {
			t.Errorf("missing project-prefixed hostname: %s", jsonStr)
		}
		// Non-project session
		if !contains(jsonStr, `no-project-ui.localhost`) {
			t.Errorf("missing non-project hostname: %s", jsonStr)
		}
		// Should not have project prefix on the non-project session
		if contains(jsonStr, `myapp-no-project`) {
			t.Errorf("non-project session incorrectly got project prefix: %s", jsonStr)
		}
	})

	t.Run("project alias is sanitized in hostname", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"my-session": {
				Name:         "my-session",
				Ports:        map[string]int{"FRONTEND": 3000},
				ProjectAlias: "My_Project",
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, _ := json.Marshal(config)
		jsonStr := string(jsonData)
		// ProjectAlias should be sanitized: "My_Project" -> "my-project"
		if !contains(jsonStr, `my-project-my-session-frontend.localhost`) {
			t.Errorf("project alias not sanitized in hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `sess-my-project-my-session-frontend`) {
			t.Errorf("project alias not sanitized in route ID: %s", jsonStr)
		}
	})

	t.Run("session with empty ports produces no routes", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"empty": {
				Name:  "empty",
				Ports: map[string]int{},
			},
		}
		config := BuildCaddyConfig(sessions)

		routes := config.Apps.HTTP.Servers["devx"].Routes
		if len(routes) != 0 {
			t.Errorf("expected 0 routes for empty ports, got %d", len(routes))
		}
	})
}

func TestBuildHostnameLabelLength(t *testing.T) {
	t.Run("short label is unchanged", func(t *testing.T) {
		h := BuildHostname("my-session", "frontend", "travally")
		label := strings.TrimSuffix(h, ".localhost")
		if len(label) > 63 {
			t.Errorf("label too long: %d chars: %s", len(label), label)
		}
		if h != "travally-my-session-frontend.localhost" {
			t.Errorf("unexpected hostname: %s", h)
		}
	})

	t.Run("label exceeding 63 chars is truncated with hash", func(t *testing.T) {
		// session: "claude-update-enrichment-loading-animation-sz8dv" (48 chars)
		// project: "travally", service: "frontend"
		// full label would be 66 chars without truncation
		h := BuildHostname(
			"claude-update-enrichment-loading-animation-sz8dv",
			"frontend",
			"travally",
		)
		label := strings.TrimSuffix(h, ".localhost")
		if len(label) > 63 {
			t.Errorf("label exceeds 63 chars: %d: %s", len(label), label)
		}
		if !strings.HasSuffix(label, "-frontend") {
			t.Errorf("label should end with service suffix: %s", label)
		}
		if !strings.HasPrefix(label, "travally-") {
			t.Errorf("label should start with project prefix: %s", label)
		}
	})

	t.Run("two distinct long session names produce different hostnames", func(t *testing.T) {
		h1 := BuildHostname("claude-update-enrichment-loading-animation-sz8dv", "frontend", "travally")
		h2 := BuildHostname("claude-update-enrichment-loading-animation-abc12", "frontend", "travally")
		if h1 == h2 {
			t.Errorf("distinct sessions should not collide: %s", h1)
		}
	})

	t.Run("label without project is also truncated", func(t *testing.T) {
		// No project alias, long session name
		h := BuildHostname("claude-update-enrichment-loading-animation-sz8dv-extra", "frontend", "")
		label := strings.TrimSuffix(h, ".localhost")
		if len(label) > 63 {
			t.Errorf("label exceeds 63 chars: %d: %s", len(label), label)
		}
		if !strings.HasSuffix(label, "-frontend") {
			t.Errorf("label should end with service suffix: %s", label)
		}
	})
}

func TestSyncRoutes(t *testing.T) {
	t.Run("writes config file", func(t *testing.T) {
		// Use a temp dir to avoid writing to real config
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)
		t.Setenv("USERPROFILE", tmpDir) // Windows: os.UserHomeDir() checks USERPROFILE

		// Create the config directory
		configDir := filepath.Join(tmpDir, ".config", "devx")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatalf("failed to create config dir: %v", err)
		}

		sessions := map[string]*SessionInfo{
			"test-session": {
				Name:  "test-session",
				Ports: map[string]int{"FRONTEND": 3000},
			},
		}

		err := SyncRoutes(sessions)
		// SyncRoutes may warn about caddy reload failing, that's OK
		if err != nil {
			t.Fatalf("SyncRoutes failed: %v", err)
		}

		// Verify config file was written
		cfgFile := filepath.Join(configDir, "caddy-config.json")
		data, err := os.ReadFile(cfgFile)
		if err != nil {
			t.Fatalf("config file not written: %v", err)
		}

		// Verify it's valid JSON with expected content
		var config CaddyConfig
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("config file is not valid JSON: %v", err)
		}

		if len(config.Apps.HTTP.Servers["devx"].Routes) != 1 {
			t.Errorf("expected 1 route, got %d", len(config.Apps.HTTP.Servers["devx"].Routes))
		}
	})

	t.Run("skips when disable_caddy is true", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)
		t.Setenv("USERPROFILE", tmpDir) // Windows: os.UserHomeDir() checks USERPROFILE

		viper.Set("disable_caddy", true)
		defer viper.Set("disable_caddy", false)

		sessions := map[string]*SessionInfo{
			"test": {Name: "test", Ports: map[string]int{"UI": 3000}},
		}

		err := SyncRoutes(sessions)
		if err != nil {
			t.Fatalf("SyncRoutes should not error when disabled: %v", err)
		}

		// Config file should NOT exist
		cfgFile := filepath.Join(tmpDir, ".config", "devx", "caddy-config.json")
		if _, err := os.Stat(cfgFile); !os.IsNotExist(err) {
			t.Error("config file should not be written when caddy is disabled")
		}
	})
}
