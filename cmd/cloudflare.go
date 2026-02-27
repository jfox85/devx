package cmd

import (
	"fmt"

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

func init() {
	rootCmd.AddCommand(cloudflareCmd)
	cloudflareCmd.AddCommand(cloudflareSyncCmd)
}

func runCloudflareSync(cmd *cobra.Command, args []string) error {
	domain := viper.GetString("external_domain")
	tunnelID := viper.GetString("cloudflare_tunnel_id")
	if domain == "" || tunnelID == "" {
		fmt.Println("Cloudflare tunnel not configured. Set external_domain and cloudflare_tunnel_id in your config.")
		return nil
	}

	if err := syncAllCloudflareRoutes(); err != nil {
		return fmt.Errorf("failed to sync cloudflare routes: %w", err)
	}

	cfgPath := viper.GetString("cloudflare_tunnel_config")
	fmt.Printf("Cloudflare config written to %s\n", cfgPath)
	return nil
}
