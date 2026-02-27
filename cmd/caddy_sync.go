package cmd

import (
	"fmt"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/cloudflare"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
	"github.com/spf13/viper"
)

// buildSessionInfoMap converts stored sessions and project registry into
// the caddy.SessionInfo map needed by CheckCaddyHealth and SyncRoutes.
func buildSessionInfoMap(store *session.SessionStore, registry *config.ProjectRegistry) map[string]*caddy.SessionInfo {
	sessionInfos := make(map[string]*caddy.SessionInfo)
	for name, sess := range store.Sessions {
		info := &caddy.SessionInfo{
			Name:  name,
			Ports: sess.Ports,
		}

		for alias, project := range registry.Projects {
			if sess.ProjectPath == project.Path {
				info.ProjectAlias = alias
				break
			}
		}

		sessionInfos[name] = info
	}
	return sessionInfos
}

// syncAllCaddyRoutes loads all sessions and syncs Caddy routes.
// This is called after session create and session remove.
func syncAllCaddyRoutes() error {
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions for Caddy sync: %w", err)
	}

	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return fmt.Errorf("failed to load project registry: %w", err)
	}

	return caddy.SyncRoutes(buildSessionInfoMap(store, registry))
}

// syncAllCloudflareRoutes regenerates the cloudflared config from current sessions.
// Skips silently if external_domain or cloudflare_tunnel_id is not configured.
func syncAllCloudflareRoutes() error {
	domain := viper.GetString("external_domain")
	tunnelID := viper.GetString("cloudflare_tunnel_id")
	if domain == "" || tunnelID == "" {
		return nil
	}

	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions for Cloudflare sync: %w", err)
	}

	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return fmt.Errorf("failed to load project registry for Cloudflare sync: %w", err)
	}

	credentialsFile := viper.GetString("cloudflare_credentials_file")
	cfgPath := viper.GetString("cloudflare_tunnel_config")

	return cloudflare.SyncTunnel(
		buildSessionInfoMap(store, registry),
		tunnelID,
		credentialsFile,
		domain,
		cfgPath,
	)
}
