package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type uiState struct {
	TUISessionView string `json:"tui_session_view,omitempty"`
}

var uiStateMu sync.Mutex

func uiStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "devx", "ui-state.json"), nil
}

func LoadTUISessionView() (string, error) {
	uiStateMu.Lock()
	defer uiStateMu.Unlock()
	state, err := loadUIState()
	if err != nil {
		return "recent", err
	}
	if state.TUISessionView != "projects" {
		return "recent", nil
	}
	return "projects", nil
}

func SetTUISessionView(view string) error {
	if view != "recent" && view != "projects" {
		return fmt.Errorf("invalid TUI session view %q", view)
	}
	uiStateMu.Lock()
	defer uiStateMu.Unlock()
	state, err := loadUIState()
	if err != nil {
		return err
	}
	state.TUISessionView = view
	return writeUIState(state)
}

func loadUIState() (uiState, error) {
	path, err := uiStatePath()
	if err != nil {
		return uiState{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return uiState{}, nil
	}
	if err != nil {
		return uiState{}, err
	}
	var state uiState
	if err := json.Unmarshal(data, &state); err != nil {
		return uiState{}, fmt.Errorf("parse UI state: %w", err)
	}
	return state, nil
}

func writeUIState(state uiState) error {
	path, err := uiStatePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ui-state-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
