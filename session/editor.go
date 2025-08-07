package session

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/viper"
)

// GetEditorCommand returns the editor command to use, checking in this order:
// 1. devx config "editor" setting
// 2. VISUAL environment variable
// 3. EDITOR environment variable
// 4. Empty string if none are set
func GetEditorCommand() string {
	// Check devx config first
	if editor := viper.GetString("editor"); editor != "" {
		return editor
	}

	// Check VISUAL environment variable
	if visual := os.Getenv("VISUAL"); visual != "" {
		return visual
	}

	// Check EDITOR environment variable
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}

	return ""
}

// LaunchEditor launches the configured editor with the given path
// It runs asynchronously and returns the process PID
func LaunchEditor(path string) (int, error) {
	editorCmd := GetEditorCommand()
	if editorCmd == "" {
		// No editor configured, skip silently
		return 0, nil
	}

	fmt.Printf("Opening editor: %s %s\n", editorCmd, path)

	// Create command
	cmd := exec.Command(editorCmd, path)

	// Start the editor process without waiting
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("failed to start editor '%s': %w", editorCmd, err)
	}

	pid := cmd.Process.Pid

	// Don't wait for the editor to exit - let it run independently
	go func() {
		// Wait for the process to finish to prevent zombie processes
		cmd.Wait()
	}()

	return pid, nil
}

// LaunchEditorForSession launches the editor for a session and updates the session metadata
func LaunchEditorForSession(sessionName, path string) error {
	pid, err := LaunchEditor(path)
	if err != nil {
		return err
	}

	if pid == 0 {
		// No editor configured
		return nil
	}

	// Update session metadata with editor PID
	store, err := LoadSessions()
	if err != nil {
		fmt.Printf("Warning: failed to load sessions for PID tracking: %v\n", err)
		return nil // Don't fail the operation for metadata issues
	}

	return store.UpdateSession(sessionName, func(s *Session) {
		s.EditorPID = pid
	})
}

// IsEditorAvailable checks if the configured editor command is available
func IsEditorAvailable() bool {
	editorCmd := GetEditorCommand()
	if editorCmd == "" {
		return false
	}

	// Try to run the editor with --version or --help to see if it exists
	cmd := exec.Command(editorCmd, "--version")
	err := cmd.Run()
	if err != nil {
		// Try --help if --version fails
		cmd = exec.Command(editorCmd, "--help")
		err = cmd.Run()
	}

	return err == nil
}

// IsProcessRunning checks if a process with the given PID is still running
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Send signal 0 to check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Signal 0 doesn't actually send a signal, just checks if process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// TerminateEditor terminates the editor process for a session
func TerminateEditor(sessionName string) error {
	store, err := LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	_, exists := store.GetSession(sessionName)
	if !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	// Note: Editor window may still be open and will need to be closed manually

	// Clear the PID from session metadata
	return store.UpdateSession(sessionName, func(s *Session) {
		s.EditorPID = 0
	})
}

// AttachEditorToSession handles editor attachment logic for session attach
func AttachEditorToSession(sessionName, path string) error {
	store, err := LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	session, exists := store.GetSession(sessionName)
	if !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	// Check if editor is still running
	if session.EditorPID > 0 && IsProcessRunning(session.EditorPID) {
		fmt.Printf("Editor is already running (PID: %d)\n", session.EditorPID)
		return nil
	}

	// Editor is not running, launch a new one
	if session.EditorPID > 0 {
		fmt.Printf("Editor process %d is no longer running, launching new instance\n", session.EditorPID)
	}

	return LaunchEditorForSession(sessionName, path)
}
