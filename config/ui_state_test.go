package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setUIStateTestUserDirectories(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	return home
}

func TestTUISessionViewPreservesUnknownStateKeys(t *testing.T) {
	home := setUIStateTestUserDirectories(t)
	path := filepath.Join(home, ".config", "devx", "ui-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"future":{"enabled":true},"tui_session_view":"recent"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTUISessionView("projects"); err != nil {
		t.Fatal(err)
	}
	var state map[string]json.RawMessage
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if _, ok := state["future"]; !ok {
		t.Fatalf("unknown state key was lost: %s", data)
	}
}

func TestTUISessionViewRecoversNullState(t *testing.T) {
	home := setUIStateTestUserDirectories(t)
	path := filepath.Join(home, ".config", "devx", "ui-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`null`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTUISessionView("projects"); err != nil {
		t.Fatalf("null state was not recovered: %v", err)
	}
	if view, err := LoadTUISessionView(); err != nil || view != "projects" {
		t.Fatalf("recovered view=%q err=%v", view, err)
	}
}

func TestTUISessionViewDoesNotQuarantineReadErrors(t *testing.T) {
	home := setUIStateTestUserDirectories(t)
	path := filepath.Join(home, ".config", "devx", "ui-state.json")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SetTUISessionView("projects"); err == nil {
		t.Fatal("expected read error")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("read-error path was quarantined: info=%v err=%v", info, err)
	}
}

func TestTUISessionViewRecoversCorruptState(t *testing.T) {
	home := setUIStateTestUserDirectories(t)
	path := filepath.Join(home, ".config", "devx", "ui-state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTUISessionView("projects"); err != nil {
		t.Fatalf("corrupt state was not recovered: %v", err)
	}
	matches, err := filepath.Glob(path + ".corrupt.*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("corrupt state was not quarantined: matches=%v err=%v", matches, err)
	}
	if view, err := LoadTUISessionView(); err != nil || view != "projects" {
		t.Fatalf("recovered view=%q err=%v", view, err)
	}
}

func TestTUISessionViewUsesDedicatedUserScopedState(t *testing.T) {
	home := setUIStateTestUserDirectories(t)
	project := t.TempDir()
	projectConfig := filepath.Join(project, ".devx")
	if err := os.MkdirAll(projectConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectConfig, "config.yaml")
	if err := os.WriteFile(configPath, []byte("web_secret_token: do-not-copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	if err := SetTUISessionView("projects"); err != nil {
		t.Fatal(err)
	}
	view, err := LoadTUISessionView()
	if err != nil || view != "projects" {
		t.Fatalf("view=%q err=%v", view, err)
	}
	projectData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(projectData) != "web_secret_token: do-not-copy\n" {
		t.Fatalf("project config was modified: %s", projectData)
	}
	statePath := filepath.Join(home, ".config", "devx", "ui-state.json")
	if info, err := os.Stat(statePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("user UI state missing or wrong mode: info=%v err=%v", info, err)
	}
}
