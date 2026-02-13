package cmd

import (
	"fmt"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
)

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

	return caddy.SyncRoutes(sessionInfos)
}
