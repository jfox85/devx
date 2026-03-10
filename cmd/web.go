//go:build !windows

package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jfox85/devx/web"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var webDaemonFlag bool

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start the devx web interface",
	Long:  `Starts a local HTTP server with a web UI for session management. Requires web_secret_token in config.`,
	RunE:  runWeb,
}

var webStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the devx web daemon",
	RunE:  runWebStop,
}

var webStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show devx web daemon status",
	RunE:  runWebStatus,
}

func init() {
	rootCmd.AddCommand(webCmd)
	webCmd.AddCommand(webStopCmd)
	webCmd.AddCommand(webStatusCmd)
	webCmd.Flags().BoolVar(&webDaemonFlag, "daemon", false, "Run as background daemon")
}

func webPIDPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "devx", "web.pid")
}

func runWeb(cmd *cobra.Command, args []string) error {
	token := viper.GetString("web_secret_token")
	port := viper.GetInt("web_port")

	if webDaemonFlag {
		// Validate token before daemonizing so errors are caught in foreground
		if token == "" {
			return fmt.Errorf("web_secret_token must be set in config to use devx web")
		}
		return startWebDaemon(port)
	}

	srv, err := web.New(token, port)
	if err != nil {
		return err
	}

	// Foreground mode: handle SIGTERM/SIGINT for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down...")
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

func startWebDaemon(port int) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable: %w", err)
	}

	pidPath := webPIDPath()
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		return fmt.Errorf("failed to create web state dir: %w", err)
	}

	cmd := exec.Command(self, "web")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	// Redirect daemon output to a log file so errors are not silently lost.
	logPath := strings.TrimSuffix(pidPath, ".pid") + ".log"
	if lf, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600); err == nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	pid := cmd.Process.Pid

	// Poll the health endpoint to confirm the child is actually listening before
	// writing the PID file and reporting success. This catches immediate failures
	// like port-already-in-use that cmd.Start() cannot detect.
	healthURL := fmt.Sprintf("http://localhost:%d/api/health", port)
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL) //nolint:noctx
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				ready = true
				break
			}
		}
		// Check if the child already exited (failed fast).
		if cmd.ProcessState != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		_ = cmd.Process.Kill()
		return fmt.Errorf("web daemon failed to start (port %d may be in use)", port)
	}

	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		fmt.Printf("Warning: could not write PID file: %v\n", err)
	}

	fmt.Printf("devx web daemon started (pid %d, port %d)\n", pid, port)
	return nil
}

func runWebStop(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(webPIDPath())
	if err != nil {
		fmt.Println("devx web is not running")
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid PID file: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop process %d: %w", pid, err)
	}

	// Wait briefly for the process to exit before removing the PID file.
	for i := 0; i < 10; i++ {
		if proc.Signal(syscall.Signal(0)) != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Only remove the PID file if the process has actually exited.
	if proc.Signal(syscall.Signal(0)) == nil {
		return fmt.Errorf("process %d did not stop within 1 second; PID file left in place", pid)
	}

	if err := os.Remove(webPIDPath()); err != nil {
		fmt.Printf("Warning: could not remove PID file: %v\n", err)
	}
	fmt.Printf("devx web stopped (pid %d)\n", pid)
	return nil
}

func runWebStatus(cmd *cobra.Command, args []string) error {
	port := viper.GetInt("web_port")
	data, err := os.ReadFile(webPIDPath())
	if err != nil {
		fmt.Println("devx web is not running")
		return nil
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Println("devx web is not running (invalid PID file)")
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(syscall.Signal(0)) != nil {
		fmt.Println("devx web is not running (stale PID file)")
		return nil
	}

	fmt.Printf("devx web is running (pid %d, port %d)\n", pid, port)
	return nil
}
