package target

import (
	"context"
	"fmt"
	"strings"

	"github.com/jfox85/devx/session"
)

// SetGatepostBypass toggles the host-owned emergency egress bypass for the
// current Docker-backed Gatepost runtime. The command layer calls this helper
// rather than manipulating runtime internals directly.
func SetGatepostBypass(ctx context.Context, meta session.TargetMeta, bypass bool) error {
	if meta.Type != "gatepost" || !meta.Gatepost.Enabled {
		return fmt.Errorf("not a gatepost target")
	}
	if meta.Gatepost.EgressNetworkName == "" || meta.ContainerName == "" {
		return fmt.Errorf("gatepost runtime metadata is incomplete")
	}
	if bypass {
		if err := dockerRun(ctx, "network", "connect", meta.Gatepost.EgressNetworkName, meta.ContainerName); err != nil && !isAlreadyConnected(err) {
			return err
		}
		_ = dockerRunIgnore(ctx, "exec", meta.ContainerName, "sh", "-lc", "tmux setenv -g -u HTTP_PROXY; tmux setenv -g -u HTTPS_PROXY; tmux setenv -g -u http_proxy; tmux setenv -g -u https_proxy")
		return nil
	}
	if err := dockerRun(ctx, "network", "disconnect", meta.Gatepost.EgressNetworkName, meta.ContainerName); err != nil && !isDockerNotFound(err) {
		return err
	}
	_ = dockerRunIgnore(ctx, "exec", meta.ContainerName, "sh", "-lc", "tmux setenv -g HTTP_PROXY http://proxy:8080; tmux setenv -g HTTPS_PROXY http://proxy:8080; tmux setenv -g http_proxy http://proxy:8080; tmux setenv -g https_proxy http://proxy:8080")
	return nil
}

func isAlreadyConnected(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exists") || strings.Contains(msg, "already connected")
}
