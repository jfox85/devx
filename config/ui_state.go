package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jfox85/devx/internal/filelock"
)

type uiState map[string]json.RawMessage

var uiStateMu sync.Mutex
var errCorruptUIState = errors.New("corrupt UI state")

func uiStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for UI state: %w", err)
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
	var view string
	if raw := state["tui_session_view"]; raw != nil {
		if err := json.Unmarshal(raw, &view); err != nil {
			return "recent", fmt.Errorf("decode TUI session view: %w", err)
		}
	}
	if view != "projects" {
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
	return withUIStateFileLock(func(path string) error {
		state, err := loadUIStateAt(path)
		if errors.Is(err, errCorruptUIState) {
			corruptPath := fmt.Sprintf("%s.corrupt.%d", path, time.Now().UnixNano())
			if renameErr := os.Rename(path, corruptPath); renameErr != nil && !os.IsNotExist(renameErr) {
				return fmt.Errorf("quarantine corrupt UI state: %w", renameErr)
			}
			state = make(uiState)
		} else if err != nil {
			return err
		}
		raw, err := json.Marshal(view)
		if err != nil {
			return fmt.Errorf("encode TUI session view: %w", err)
		}
		state["tui_session_view"] = raw
		return writeUIStateAt(path, state)
	})
}

func loadUIState() (uiState, error) {
	path, err := uiStatePath()
	if err != nil {
		return nil, err
	}
	return loadUIStateAt(path)
}

func loadUIStateAt(path string) (uiState, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(uiState), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read UI state: %w", err)
	}
	state := make(uiState)
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("%w: parse JSON: %v", errCorruptUIState, err)
	}
	if state == nil {
		return nil, fmt.Errorf("%w: expected JSON object", errCorruptUIState)
	}
	return state, nil
}

func withUIStateFileLock(fn func(path string) error) error {
	path, err := uiStatePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create UI state directory: %w", err)
	}
	lock, err := filelock.Acquire(path + ".lock")
	if err != nil {
		return fmt.Errorf("acquire UI state lock: %w", err)
	}
	defer lock.Release()
	return fn(path)
}

func writeUIStateAt(path string, state uiState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode UI state: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ui-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create UI state temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod UI state temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write UI state temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync UI state temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close UI state temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish UI state: %w", err)
	}
	return nil
}
