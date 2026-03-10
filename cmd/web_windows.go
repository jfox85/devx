//go:build windows

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

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
	if webDaemonFlag {
		return fmt.Errorf("daemon mode not supported on Windows")
	}

	token := viper.GetString("web_secret_token")
	port := viper.GetInt("web_port")

	srv, err := web.New(token, port)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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

func runWebStop(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("web daemon not supported on Windows")
}

func runWebStatus(cmd *cobra.Command, args []string) error {
	fmt.Println("web daemon not supported on Windows")
	return nil
}
