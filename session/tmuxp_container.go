package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnsureTmuxSessionInContainer ensures the named tmux session is alive on the
// host tmux server, with each pane running via `docker exec` into the container.
// The .tmuxp.yaml uses /workspace paths that only exist inside the container, so
// we rewrite it to wrap every pane command with `docker exec -it <container> bash -lc`
// before loading it with host-side tmuxp.
func EnsureTmuxSessionInContainer(sessionName, containerName string) error {
	if exec.Command("tmux", "has-session", "-t", "="+sessionName).Run() == nil {
		// Session exists — verify it's running docker exec panes.
		// If a previous failed attempt left a host-mode tmux session,
		// kill it so we can recreate with container-wrapped panes.
		out, err := exec.Command("tmux", "list-panes", "-t", "="+sessionName, "-F", "#{pane_current_command}").Output()
		if err == nil {
			hasDocker := false
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "docker" {
					hasDocker = true
					break
				}
			}
			if hasDocker {
				return nil // already alive with container panes
			}
			// Host-mode session — kill and recreate
			_ = exec.Command("tmux", "kill-session", "-t", "="+sessionName).Run()
		} else {
			return nil // can't inspect, assume it's fine
		}
	}
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
	if err := rewriteTmuxpForContainer(worktreePath, containerName); err != nil {
		return fmt.Errorf("rewrite .tmuxp.yaml: %w", err)
	}
	// Force the DevX session name so it matches regardless of what
	// session_name is set to in the .tmuxp.yaml display name.
	return launchTmuxDetached(worktreePath, sessionName)
}

// rewriteTmuxpForContainer rewrites .tmuxp.yaml so tmux runs on the host
// but every pane execs into the container via docker exec.
func rewriteTmuxpForContainer(worktreePath, containerName string) error {
	tmuxpPath := filepath.Join(worktreePath, ".tmuxp.yaml")
	data, err := os.ReadFile(tmuxpPath)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	cfg["start_directory"] = worktreePath
	windows, _ := cfg["windows"].([]interface{})
	for _, w := range windows {
		win, ok := w.(map[string]interface{})
		if !ok {
			continue
		}
		var before []string
		if b, ok := win["shell_command_before"]; ok {
			before = collectStrings(b)
			delete(win, "shell_command_before")
		}
		panes, _ := win["panes"].([]interface{})
		for i, p := range panes {
			switch pane := p.(type) {
			case string:
				panes[i] = wrapInContainer(containerName, before, pane)
			case map[string]interface{}:
				if sc, ok := pane["shell_command"].(string); ok {
					pane["shell_command"] = wrapInContainer(containerName, before, sc)
				} else {
					pane["shell_command"] = wrapInContainer(containerName, before, "bash -l")
				}
				delete(pane, "shell_command_before")
				panes[i] = pane
			}
		}
		win["panes"] = panes
	}
	cfg["windows"] = windows
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(tmuxpPath, out, 0644)
}

func wrapInContainer(containerName string, before []string, cmd string) string {
	parts := append(before, cmd)
	inner := strings.Join(parts, " && ")
	escaped := strings.ReplaceAll(inner, "'", "'\"'\"'")
	return fmt.Sprintf("docker exec -it %s bash -lc '%s'", containerName, escaped)
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
