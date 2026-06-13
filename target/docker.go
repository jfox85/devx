package target

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/session"
)

// DockerTarget runs sessions inside Docker containers.
type DockerTarget struct{}

func (d *DockerTarget) Type() string { return "docker" }

func (d *DockerTarget) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
	name := ContainerName(opts.SessionName)
	netName := NetworkName(opts.SessionName)

	// Create per-session bridge network
	if err := dockerRun(ctx, "network", "create", netName); err != nil {
		return nil, fmt.Errorf("create network: %w", err)
	}

	// Build docker run arguments
	args := []string{
		"run", "-d",
		"--name", name,
		"--network", netName,
		"--restart", "unless-stopped",
		"-v", opts.WorktreePath + ":/workspace",
		"-w", "/workspace",
	}

	// Publish ports on loopback
	for _, port := range opts.HostPorts {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port))
	}

	// Security
	sec := opts.Security
	if sec.MemoryLimit == "" {
		sec = DefaultSecurityOpts()
	}
	for _, cap := range sec.CapDrop {
		args = append(args, "--cap-drop="+cap)
	}
	if sec.NoNewPrivs {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	if sec.MemoryLimit != "" {
		args = append(args, "--memory", sec.MemoryLimit)
	}
	if sec.CPULimit != "" {
		args = append(args, "--cpus", sec.CPULimit)
	}
	if sec.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", sec.PidsLimit))
	}
	if sec.ReadOnlyRoot {
		args = append(args, "--read-only")
		for _, m := range sec.TmpfsMounts {
			args = append(args, "--tmpfs", m)
		}
	}

	// Environment
	for k, v := range opts.Env {
		args = append(args, "-e", k+"="+v)
	}

	// Labels
	for k, v := range opts.Labels {
		args = append(args, "--label", k+"="+v)
	}

	// Image + command
	image := opts.Image
	if image == "" {
		image = "devx-session-base:latest"
	}
	args = append(args, image, "sleep", "infinity")

	if err := dockerRun(ctx, args...); err != nil {
		// Clean up network on failure
		_ = dockerRunIgnore(ctx, "network", "rm", netName)
		return nil, fmt.Errorf("create container: %w", err)
	}

	// Get container ID
	containerID, err := dockerOutput(ctx, "inspect", "--format", "{{.Id}}", name)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	return &StartResult{
		Meta: session.TargetMeta{
			Type:          "docker",
			ContainerID:   containerID,
			ContainerName: name,
			NetworkName:   netName,
			Image:         image,
		},
	}, nil
}

func (d *DockerTarget) Stop(ctx context.Context, meta session.TargetMeta) error {
	var errs []string
	if meta.ContainerName != "" {
		// Stop then force-remove; ignore "no such container" but report other errors.
		_ = dockerRunIgnore(ctx, "stop", meta.ContainerName)
		if err := dockerRun(ctx, "rm", "-f", meta.ContainerName); err != nil {
			if !isDockerNotFound(err) {
				errs = append(errs, fmt.Sprintf("rm container: %v", err))
			}
		}
	}
	if meta.NetworkName != "" {
		if err := dockerRun(ctx, "network", "rm", meta.NetworkName); err != nil {
			if !isDockerNotFound(err) {
				errs = append(errs, fmt.Sprintf("rm network: %v", err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("docker teardown: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (d *DockerTarget) IsRunning(meta session.TargetMeta) bool { return IsDockerRunning(meta) }

func (d *DockerTarget) EnsureTmuxSession(name string, sess *session.Session) error {
	if sess.Target.ContainerName == "" {
		return fmt.Errorf("docker session %q has no runtime container", name)
	}
	return session.EnsureTmuxSessionInContainer(name, sess.Target.ContainerName, sess)
}

func (d *DockerTarget) AttachTmuxSession(name string, sess *session.Session) error {
	if err := d.EnsureTmuxSession(name, sess); err != nil {
		return err
	}
	return session.AttachTmuxSession(name)
}

func (d *DockerTarget) KillTmuxServer(meta session.TargetMeta) error {
	if meta.ContainerName == "" {
		return nil
	}
	return ExecInSession(meta, []string{"tmux", "kill-server"}, false).Run()
}

// isDockerNotFound checks if a docker error is a "not found" error.
func isDockerNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such") || strings.Contains(msg, "not found")
}

// IsDockerRunning checks if a Docker container is alive.
func IsDockerRunning(meta session.TargetMeta) bool {
	if meta.ContainerName == "" {
		return false
	}
	out, err := dockerOutput(context.Background(), "inspect", "--format", "{{.State.Running}}", meta.ContainerName)
	if err != nil {
		return false
	}
	return out == "true"
}

// ContainerName returns the Docker container name for a session.
func ContainerName(sessionName string) string {
	return "devx-" + caddy.SanitizeHostname(sessionName)
}

// NetworkName returns the Docker network name for a session.
func NetworkName(sessionName string) string {
	return "devx-" + caddy.SanitizeHostname(sessionName) + "-net"
}

// CheckAvailable returns nil if Docker is running, or an error with a clear message.
func CheckAvailable() error {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Docker is not running. Start Docker Desktop or install Docker to use --target docker")
	}
	return nil
}

// ImageExists checks if a Docker image exists locally.
func ImageExists(image string) bool {
	cmd := exec.Command("docker", "image", "inspect", image)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// BuildImage builds a Docker image from the given context directory.
func BuildImage(ctx context.Context, tag, contextDir string) error {
	return dockerRun(ctx, "build", "-t", tag, contextDir)
}

// dockerRun executes a docker command and returns any error.
func dockerRun(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

// dockerRunIgnore executes a docker command and silently ignores errors.
func dockerRunIgnore(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// dockerOutput executes a docker command and returns trimmed stdout.
func dockerOutput(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}
