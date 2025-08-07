package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// RunCleanupCommand executes the configured cleanup command with session environment variables
func RunCleanupCommand(sess *Session) error {
	cleanupCmd := viper.GetString("cleanup_command")
	if cleanupCmd == "" {
		return nil // No cleanup command configured
	}

	fmt.Printf("Running cleanup command...\n")

	// Prepare environment variables for the cleanup command
	env := prepareCleanupEnvironment(sess)

	// Execute the cleanup command
	if err := executeCleanupCommand(cleanupCmd, sess.Path, env); err != nil {
		return fmt.Errorf("cleanup command failed: %w", err)
	}

	fmt.Printf("Cleanup command completed successfully\n")
	return nil
}

// prepareCleanupEnvironment creates environment variables for the cleanup command
func prepareCleanupEnvironment(sess *Session) []string {
	// Start with current environment
	env := os.Environ()

	// Add session name
	env = append(env, fmt.Sprintf("SESSION_NAME=%s", sess.Name))

	// Add port variables
	for serviceName, port := range sess.Ports {
		// Convert service name to PORT variable name
		// e.g., "ui" -> "UI_PORT", "auth-service" -> "AUTH_SERVICE_PORT"
		portVar := strings.ToUpper(serviceName)
		portVar = strings.ReplaceAll(portVar, "-", "_") + "_PORT"
		env = append(env, fmt.Sprintf("%s=%d", portVar, port))
	}

	// Add hostname variables if routes exist
	if len(sess.Routes) > 0 {
		for serviceName := range sess.Routes {
			// Convert service name to HOST variable name
			// e.g., "ui" -> "UI_HOST", "auth-service" -> "AUTH_SERVICE_HOST"
			hostVar := strings.ToUpper(serviceName)
			hostVar = strings.ReplaceAll(hostVar, "-", "_") + "_HOST"

			// Reconstruct the hostname from the route ID
			// Route IDs are typically in format: "session-service.localhost"
			hostname := fmt.Sprintf("https://%s-%s.localhost", sess.Name, strings.ToLower(serviceName))
			env = append(env, fmt.Sprintf("%s=%s", hostVar, hostname))
		}
	}

	// Add worktree path
	env = append(env, fmt.Sprintf("WORKTREE_PATH=%s", sess.Path))

	// Add session branch
	env = append(env, fmt.Sprintf("SESSION_BRANCH=%s", sess.Branch))

	return env
}

// executeCleanupCommand runs the cleanup command with the specified environment
func executeCleanupCommand(command, workingDir string, env []string) error {
	// Split the command into parts for proper execution
	// This is a simple split - for more complex commands, consider using shell
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty cleanup command")
	}

	// Create the command
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workingDir
	cmd.Env = env

	// Capture output for debugging
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set a reasonable timeout for cleanup operations
	timeout := 30 * time.Second

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cleanup command: %w", err)
	}

	// Wait for completion with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("cleanup command exited with error: %w", err)
		}
		return nil
	case <-time.After(timeout):
		// Kill the process if it's taking too long
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return fmt.Errorf("cleanup command timed out after %v", timeout)
	}
}

// RunCleanupCommandForShell executes cleanup command through shell for complex commands
func RunCleanupCommandForShell(sess *Session) error {
	cleanupCmd := viper.GetString("cleanup_command")
	if cleanupCmd == "" {
		return nil // No cleanup command configured
	}

	fmt.Printf("Running cleanup command through shell...\n")

	// Prepare environment variables
	env := prepareCleanupEnvironment(sess)

	// Execute through shell for complex commands with pipes, redirects, etc.
	cmd := exec.Command("sh", "-c", cleanupCmd)
	cmd.Dir = sess.Path
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set timeout
	timeout := 30 * time.Second

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cleanup command: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("cleanup command failed: %w", err)
		}
		fmt.Printf("Cleanup command completed successfully\n")
		return nil
	case <-time.After(timeout):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return fmt.Errorf("cleanup command timed out after %v", timeout)
	}
}
