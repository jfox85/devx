package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTUISessionViewUsesDedicatedUserScopedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	projectConfig := filepath.Join(project, ".devx")
	if err := os.MkdirAll(projectConfig, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectConfig, "config.yaml")
	if err := os.WriteFile(configPath, []byte("web_secret_token: do-not-copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDir, _ := os.Getwd()
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
