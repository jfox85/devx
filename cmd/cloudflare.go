package cmd

import (
	"fmt"

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

func init() {
	rootCmd.AddCommand(cloudflareCmd)
	cloudflareCmd.AddCommand(cloudflareSyncCmd)
	cloudflareCmd.AddCommand(cloudflareCheckCmd)
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
	printCheckLine("tunnel daemon running", result.TunnelRunning, result.TunnelError)
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
