package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EnsureTmuxSessionInContainer ensures the named tmux session is alive on the
// host tmux server, with every pane running inside the container via docker exec.
//
// The approach:
// 1. Read the project's .tmuxp.yaml to discover window/pane structure and commands
// 2. Create the tmux session directly (no tmuxp) with guard scripts that:
//   - Verify the container is running before each exec
//   - Auto-reconnect when docker exec exits (Ctrl-C, crash, etc.)
//   - Never drop to a host shell — show error + wait instead
//
// 3. Verify post-launch that panes are actually inside the container
//
// The project's .tmuxp.yaml is never modified — we read it as a declarative
// spec and build the tmux session ourselves.
func EnsureTmuxSessionInContainer(sessionName, containerName string, sess *Session) error {
	// Pre-flight: verify the container is running.
	if err := verifyContainerRunning(containerName); err != nil {
		return fmt.Errorf("container %q not running: %w", containerName, err)
	}

	if exec.Command("tmux", "has-session", "-t", "="+sessionName).Run() == nil {
		// Session exists — verify panes are running inside the container.
		if err := verifyPanesInContainer(sessionName, containerName); err == nil {
			return nil // all good
		}
		// Panes are not in the container (host-mode leftover or stale).
		// Kill and recreate.
		_ = exec.Command("tmux", "kill-session", "-t", "="+sessionName).Run()
	}

	if err := createContainerTmuxSession(sessionName, containerName, sess); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}

	// Post-launch verification: confirm panes are actually in the container.
	// docker exec takes a moment to start; 3s is enough for the container
	// state check + exec handshake.
	time.Sleep(3 * time.Second)
	if err := verifyPanesInContainer(sessionName, containerName); err != nil {
		// Kill the broken session — do not leave host-mode panes alive.
		_ = exec.Command("tmux", "kill-session", "-t", "="+sessionName).Run()
		return fmt.Errorf("tmux session launched but panes are not inside container: %w", err)
	}

	return nil
}

// verifyContainerRunning checks that the named container exists and is running.
func verifyContainerRunning(containerName string) error {
	out, err := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", containerName).Output()
	if err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}
	if strings.TrimSpace(string(out)) != "true" {
		return fmt.Errorf("container is not running (state: %s)", strings.TrimSpace(string(out)))
	}
	return nil
}

// verifyPanesInContainer checks that every pane in the tmux session is
// running in a safe state: either actively running docker exec into the
// expected container, or running the guard script (waiting at reconnect
// prompt after a service exit). A pane running an unguarded host shell
// is the security-critical failure case.
func verifyPanesInContainer(sessionName, containerName string) error {
	out, err := exec.Command("tmux", "list-panes", "-s", "-t", "="+sessionName,
		"-F", "#{pane_pid}").Output()
	if err != nil {
		return fmt.Errorf("list panes: %w", err)
	}

	pids := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(pids) == 0 || pids[0] == "" {
		return fmt.Errorf("no panes found")
	}

	// The exact docker exec prefix we expect to find in child processes.
	execPrefix := "docker exec -it " + containerName + " "
	guardDir := hostTmuxDir(sessionName)

	// Every pane must be in a safe state:
	// 1. Running docker exec targeting our container, OR
	// 2. Running the guard script (e.g., service exited, waiting at prompt)
	//
	// Unsafe state: a pane shell that's NOT our guard script and NOT running
	// docker exec — this would be a host shell with arbitrary access.
	var dockerExecCount int
	for i, pid := range pids {
		pid = strings.TrimSpace(pid)
		if pid == "" {
			continue
		}

		// Check if the pane's command is our guard script.
		psOut, _ := exec.Command("ps", "-o", "command=", "-p", pid).Output()
		cmdLine := strings.TrimSpace(string(psOut))

		isGuardScript := strings.Contains(cmdLine, guardDir)
		hasDockerExec := paneHasDockerExec(pid, execPrefix)

		if hasDockerExec {
			dockerExecCount++
		} else if !isGuardScript {
			// Neither running docker exec nor our guard script — unsafe.
			return fmt.Errorf("pane %d (pid %s, cmd %q) is not running docker exec or guard script for %q",
				i, pid, cmdLine, containerName)
		}
		// Guard script without docker exec child is OK — service may have
		// exited and the pane is at the reconnect prompt.
	}

	// At least one pane must be actively exec'd into the container.
	// This catches the case where ALL panes failed but are at reconnect prompts.
	if dockerExecCount == 0 {
		return fmt.Errorf("no pane is actively running docker exec for %q (all at reconnect prompt)", containerName)
	}

	return nil
}

// paneHasDockerExec checks whether a pane's child process tree contains
// a docker exec command matching the expected prefix.
func paneHasDockerExec(panePid, execPrefix string) bool {
	// Find child PIDs of the pane shell.
	// Use ps (not pgrep -la) because macOS pgrep doesn't show full command lines.
	childPids, err := exec.Command("pgrep", "-P", panePid).Output()
	if err != nil {
		return false
	}
	for _, cpid := range strings.Split(strings.TrimSpace(string(childPids)), "\n") {
		cpid = strings.TrimSpace(cpid)
		if cpid == "" {
			continue
		}
		psOut, err := exec.Command("ps", "-o", "command=", "-p", cpid).Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(psOut), execPrefix) {
			return true
		}
	}
	return false
}

// tmuxpWindow represents a window parsed from .tmuxp.yaml.
type tmuxpWindow struct {
	Name   string
	Before []string // shell_command_before entries
	Panes  []string // pane commands
}

// parseTmuxpWindows reads the project's .tmuxp.yaml and extracts the window/pane
// structure. Commands are expected to be container-internal (e.g. /workspace paths).
// Any existing docker-exec wrapping is detected and stripped.
func parseTmuxpWindows(worktreePath string) ([]tmuxpWindow, error) {
	tmuxpPath := filepath.Join(worktreePath, ".tmuxp.yaml")
	data, err := os.ReadFile(tmuxpPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", tmuxpPath, err)
	}

	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", tmuxpPath, err)
	}

	windows, _ := cfg["windows"].([]interface{})
	var result []tmuxpWindow
	for _, w := range windows {
		win, ok := w.(map[string]interface{})
		if !ok {
			continue
		}

		tw := tmuxpWindow{
			Name: stringVal(win, "window_name"),
		}
		if b, ok := win["shell_command_before"]; ok {
			tw.Before = collectStrings(b)
		}

		panes, _ := win["panes"].([]interface{})
		for _, p := range panes {
			switch pane := p.(type) {
			case string:
				tw.Panes = append(tw.Panes, stripDockerWrap(pane))
			case map[string]interface{}:
				if sc, ok := pane["shell_command"].(string); ok {
					tw.Panes = append(tw.Panes, stripDockerWrap(sc))
				} else {
					tw.Panes = append(tw.Panes, "bash -l")
				}
			}
		}
		if len(tw.Panes) == 0 {
			tw.Panes = []string{"bash -l"}
		}
		result = append(result, tw)
	}
	return result, nil
}

// stripDockerWrap removes any docker exec wrapping from a pane command,
// returning the inner command that should run inside the container.
// This handles the corrupted case where .tmuxp.yaml was rewritten in-place
// with single, double, or triple docker exec wrapping.
func stripDockerWrap(cmd string) string {
	// Keep stripping layers of docker exec wrapping until we reach the inner command.
	for i := 0; i < 10; i++ { // safety limit
		inner, ok := extractDockerExecInner(cmd)
		if !ok {
			return cmd
		}
		cmd = inner
	}
	return cmd
}

// extractDockerExecInner tries to extract the inner command from a docker exec wrapper.
// Returns (inner, true) if wrapping was found, (original, false) otherwise.
func extractDockerExecInner(cmd string) (string, bool) {
	// Match patterns like:
	//   docker exec -it <name> bash -lc '<inner>'
	//   bash -c 'while true; do ... docker exec -it <name> bash -lc '<inner>'; ...'
	idx := strings.Index(cmd, "docker exec")
	if idx < 0 {
		return cmd, false
	}

	// Find "bash -lc '" after "docker exec"
	bashIdx := strings.Index(cmd[idx:], "bash -lc '")
	if bashIdx < 0 {
		return cmd, false
	}
	innerStart := idx + bashIdx + len("bash -lc '")

	// Find the matching closing quote, handling ''"'"'' escaping
	inner := unescapeBashSingleQuote(cmd[innerStart:])
	if inner == "" {
		return cmd, false
	}
	return strings.TrimSpace(inner), true
}

// unescapeBashSingleQuote extracts content from a single-quoted bash string,
// handling the '\"'\"' escape pattern for embedded single quotes.
func unescapeBashSingleQuote(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\'' {
			// Check for ''"'"'' pattern (escaped single quote)
			if strings.HasPrefix(s[i:], "'\"'\"'") {
				result.WriteByte('\'')
				i += 5
				continue
			}
			// End of single-quoted string
			return result.String()
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

func stringVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// createContainerTmuxSession creates a tmux session on the host where every
// pane runs a guarded docker exec into the container. Uses tmux commands
// directly (not tmuxp) to avoid YAML quoting issues.
func createContainerTmuxSession(sessionName, containerName string, sess *Session) error {
	// Find the worktree path from the container's /workspace mount.
	out, err := exec.Command("docker", "inspect", containerName,
		"--format", `{{range .Mounts}}{{if eq .Destination "/workspace"}}{{.Source}}{{end}}{{end}}`).Output()
	if err != nil {
		return fmt.Errorf("docker inspect %q: %w", containerName, err)
	}
	worktreePath := strings.TrimSpace(string(out))
	if worktreePath == "" {
		return fmt.Errorf("could not find /workspace mount for container %q", containerName)
	}

	windows, err := parseTmuxpWindows(worktreePath)
	if err != nil {
		return fmt.Errorf("parse tmuxp config: %w", err)
	}
	if len(windows) == 0 {
		return fmt.Errorf("no windows found in .tmuxp.yaml")
	}

	// Write guard scripts to a host-side directory. Using script files avoids
	// all the nested quoting issues of passing commands through tmux send-keys.
	guardDir := hostTmuxDir(sessionName)
	if err := os.MkdirAll(guardDir, 0o755); err != nil {
		return err
	}

	// Create the tmux session with the first window.
	firstWin := windows[0]
	firstScript, err := writeGuardScript(guardDir, containerName, "w0-p0", firstWin.Before, firstWin.Panes[0])
	if err != nil {
		return err
	}

	createCmd := exec.Command("tmux", "new-session", "-d",
		"-s", sessionName,
		"-n", firstWin.Name,
		"-c", guardDir,
		"-x", "120", "-y", "40",
		firstScript)
	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w\n%s", err, output)
	}

	// Add remaining panes to the first window.
	for j := 1; j < len(firstWin.Panes); j++ {
		script, err := writeGuardScript(guardDir, containerName,
			fmt.Sprintf("w0-p%d", j), firstWin.Before, firstWin.Panes[j])
		if err != nil {
			return err
		}
		splitCmd := exec.Command("tmux", "split-window", "-c", guardDir, "-t", "="+sessionName+":"+firstWin.Name, script)
		if output, err := splitCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux split-window: %w\n%s", err, output)
		}
	}
	// Apply tiled layout for the first window.
	_ = exec.Command("tmux", "select-layout", "-t", "="+sessionName+":"+firstWin.Name, "tiled").Run()

	// Add subsequent windows.
	for i := 1; i < len(windows); i++ {
		w := windows[i]
		script, err := writeGuardScript(guardDir, containerName,
			fmt.Sprintf("w%d-p0", i), w.Before, w.Panes[0])
		if err != nil {
			return err
		}
		newWinCmd := exec.Command("tmux", "new-window", "-c", guardDir, "-t", "="+sessionName, "-n", w.Name, script)
		if output, err := newWinCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux new-window %q: %w\n%s", w.Name, err, output)
		}

		for j := 1; j < len(w.Panes); j++ {
			script, err := writeGuardScript(guardDir, containerName,
				fmt.Sprintf("w%d-p%d", i, j), w.Before, w.Panes[j])
			if err != nil {
				return err
			}
			splitCmd := exec.Command("tmux", "split-window", "-c", guardDir, "-t", "="+sessionName+":"+w.Name, script)
			if output, err := splitCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("tmux split-window: %w\n%s", err, output)
			}
		}
		_ = exec.Command("tmux", "select-layout", "-t", "="+sessionName+":"+w.Name, "tiled").Run()
	}

	// Select the first window.
	_ = exec.Command("tmux", "select-window", "-t", "="+sessionName+":"+windows[0].Name).Run()

	return nil
}

// writeGuardScript writes a bash script that:
// 1. Checks the container is running before exec'ing
// 2. Runs docker exec with the pane command
// 3. On exit, shows a reconnect prompt instead of dropping to host
//
// Using a script file avoids all nested quoting issues with tmux + bash + docker.
func writeGuardScript(dir, containerName, id string, before []string, cmd string) (string, error) {
	parts := append(before, cmd)
	inner := strings.Join(parts, " && ")

	script := fmt.Sprintf(`#!/bin/bash
# Guard script for container pane — DO NOT run commands outside docker exec.
# If this script exits, the pane shows a reconnect prompt.
set -e
CONTAINER=%q
while true; do
    state=$(docker inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null || echo "missing")
    if [ "$state" != "true" ]; then
        echo ""
        echo "ERROR: Container $CONTAINER is not running (state: $state)"
        echo "Start the container or press Ctrl-C to close this pane."
        echo "Press Enter to retry..."
        read -r
        continue
    fi
    docker exec -it "$CONTAINER" bash -lc %q || true
    echo ""
    echo "=== Container session exited. Press Enter to reconnect, Ctrl-C to close ==="
    read -r
done
`, containerName, inner)

	path := filepath.Join(dir, id+".sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// hostTmuxDir returns the host-side directory for a session's guard scripts.
func hostTmuxDir(sessionName string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	// Sanitize session name for use as directory name.
	safe := strings.ReplaceAll(sessionName, "/", "--")
	return filepath.Join(home, ".devx", "tmux", safe)
}

func collectStrings(v interface{}) []string {
	switch val := v.(type) {
	case string:
		return []string{val}
	case []interface{}:
		var out []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
