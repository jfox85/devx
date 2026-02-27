package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "devx", "web.pid")
}

func runWeb(cmd *cobra.Command, args []string) error {
	token := viper.GetString("web_secret_token")
	port := viper.GetInt("web_port")

	srv, err := web.New(token, port)
	if err != nil {
		return err
	}

	if webDaemonFlag {
		return startWebDaemon(port)
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

	cmd := exec.Command(self, "web")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	pidPath := webPIDPath()
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		fmt.Printf("Warning: could not write PID file: %v\n", err)
	}

	fmt.Printf("devx web daemon started (pid %d, port %d)\n", cmd.Process.Pid, port)
	return nil
}

func runWebStop(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(webPIDPath())
	if err != nil {
		return fmt.Errorf("devx web is not running (no PID file found)")
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

	os.Remove(webPIDPath())
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

	pid := strings.TrimSpace(string(data))
	fmt.Printf("devx web is running (pid %s, port %d)\n", pid, port)
	return nil
}
