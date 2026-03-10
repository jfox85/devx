package cmd

import (
	"fmt"
	"os"

	"github.com/jfox85/devx/cloudflare"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cloudflareCmd = &cobra.Command{
	Use:   "cloudflare",
	Short: "Manage Cloudflare tunnel config for development sessions",
	Long:  `Commands for managing the cloudflared ingress config for external domain routing.`,
}

var cloudflareSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Regenerate cloudflared config from current sessions",
	Long:  `Regenerates the cloudflared ingress config file based on current active sessions.`,
	RunE:  runCloudflareSync,
}

var cloudflareCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check Cloudflare tunnel setup and verify ingress rules",
	RunE:  runCloudflareCheck,
}

var cloudflareStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start cloudflared tunnel daemon",
	RunE:  runCloudflareStart,
}

var cloudflareStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop cloudflared tunnel daemon",
	RunE:  runCloudflareStop,
}

var cloudflareStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cloudflared tunnel daemon status",
	RunE:  runCloudflareStatus,
}

func init() {
	rootCmd.AddCommand(cloudflareCmd)
	cloudflareCmd.AddCommand(cloudflareSyncCmd)
	cloudflareCmd.AddCommand(cloudflareCheckCmd)
	cloudflareCmd.AddCommand(cloudflareStartCmd)
	cloudflareCmd.AddCommand(cloudflareStopCmd)
	cloudflareCmd.AddCommand(cloudflareStatusCmd)
}

func runCloudflareSync(cmd *cobra.Command, args []string) error {
	domain := viper.GetString("external_domain")
	tunnelID := viper.GetString("cloudflare_tunnel_id")
	if domain == "" || tunnelID == "" {
		return fmt.Errorf("cloudflare tunnel not configured: set external_domain and cloudflare_tunnel_id in your config")
	}

	if err := syncAllCloudflareRoutes(); err != nil {
		return fmt.Errorf("failed to sync cloudflare routes: %w", err)
	}

	cfgPath := viper.GetString("cloudflare_tunnel_config")
	fmt.Printf("Cloudflare config written to %s\n", cfgPath)
	return nil
}

func runCloudflareCheck(cmd *cobra.Command, args []string) error {
	domain := viper.GetString("external_domain")
	tunnelID := viper.GetString("cloudflare_tunnel_id")
	cfgPath := viper.GetString("cloudflare_tunnel_config")
	if cfgPath == "" {
		return fmt.Errorf("cloudflare_tunnel_config is not set")
	}

	if domain == "" || tunnelID == "" {
		return fmt.Errorf("cloudflare tunnel not configured: set external_domain and cloudflare_tunnel_id in your config")
	}

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}
	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return fmt.Errorf("failed to load project registry: %w", err)
	}

	sessionInfos := buildSessionInfoMap(store, registry)
	result := cloudflare.CheckTunnel(sessionInfos, tunnelID, domain, cfgPath)

	fmt.Println("=== Cloudflare Tunnel Status ===")
	printCheckLine("cloudflared binary installed", result.BinaryInstalled, "")
	printCheckLine("tunnel registered in Cloudflare", result.TunnelExists, result.TunnelExistsError)
	printCheckLine("config file exists", result.ConfigExists, "")
	printCheckLine("config file valid", result.ConfigValid, result.ConfigError)
	printCheckLine("ingress rules match sessions", !result.IngressMismatch, "")
	if result.IngressMismatch {
		for _, h := range result.MissingRules {
			fmt.Printf("  missing: %s\n", h)
		}
		fmt.Println("  Run 'devx cloudflare sync' to fix.")
	}
	printCheckLine("DNS wildcard resolves", result.DNSValid, result.DNSError)
	if !result.DNSValid && domain != "" {
		fmt.Printf("  Ensure a wildcard CNAME record *.%s -> <tunnel-id>.cfargotunnel.com exists in Cloudflare DNS.\n", domain)
	}

	return nil
}

func printCheckLine(label string, ok bool, errMsg string) {
	if ok {
		fmt.Printf("[OK] %s\n", label)
	} else if errMsg != "" {
		fmt.Printf("[FAIL] %s: %s\n", label, errMsg)
	} else {
		fmt.Printf("[FAIL] %s\n", label)
	}
}

func runCloudflareStart(cmd *cobra.Command, args []string) error {
	tunnelID := viper.GetString("cloudflare_tunnel_id")
	domain := viper.GetString("external_domain")
	if domain == "" || tunnelID == "" {
		return fmt.Errorf("cloudflare tunnel not configured: set external_domain and cloudflare_tunnel_id in your config")
	}

	cfgPath := viper.GetString("cloudflare_tunnel_config")
	pidPath := cloudflare.DefaultPIDPath()

	// Sync config before starting so ingress rules are current
	if err := syncAllCloudflareRoutes(); err != nil {
		return fmt.Errorf("failed to sync cloudflare config: %w", err)
	}

	pid, err := cloudflare.StartDaemon(cfgPath, tunnelID, pidPath)
	if err != nil {
		return err
	}

	fmt.Printf("cloudflared started (pid %d)\n", pid)
	fmt.Printf("logs: %s\n", pidPath[:len(pidPath)-4]+".log")
	return nil
}

func runCloudflareStop(cmd *cobra.Command, args []string) error {
	pidPath := cloudflare.DefaultPIDPath()
	pid, err := cloudflare.StopDaemon(pidPath)
	if err != nil {
		return err
	}
	fmt.Printf("cloudflared stopped (pid %d)\n", pid)
	return nil
}

func runCloudflareStatus(cmd *cobra.Command, args []string) error {
	pidPath := cloudflare.DefaultPIDPath()
	pid, err := cloudflare.ReadPID(pidPath)
	if err != nil {
		fmt.Println("cloudflared is not running")
		return nil
	}
	if cloudflare.IsRunning(pid) {
		fmt.Printf("cloudflared is running (pid %d)\n", pid)
	} else {
		fmt.Printf("cloudflared is not running (stale pid %d)\n", pid)
		_ = os.Remove(pidPath)
	}
	return nil
}
