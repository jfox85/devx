package target

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type gatepostLogsProcess struct {
	PublicURL string
	Token     string
	PID       int
}

func startGatepostLogs(ctx context.Context, cfg GatepostRuntimeConfig, gatepostRoot, auditLog string, port int) (gatepostLogsProcess, error) {
	cmdName, args, dir, err := gatepostLogsCommand(cfg, gatepostRoot, auditLog, port)
	if err != nil {
		return gatepostLogsProcess{}, err
	}
	// Use exec.Command (not CommandContext) so the logs process is not killed
	// when the parent session-create command exits or times out. Detach from
	// the parent process group so signals don't cascade.
	cmd := exec.Command(cmdName, args...)
	cmd.Dir = dir
	detachGatepostLogsProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return gatepostLogsProcess{}, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return gatepostLogsProcess{}, err
	}
	result := gatepostLogsProcess{PublicURL: fmt.Sprintf("http://127.0.0.1:%d", port), PID: cmd.Process.Pid}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			for _, p := range strings.Fields(s.Text()) {
				if strings.HasPrefix(p, "http://") {
					if u, err := url.Parse(p); err == nil {
						result.PublicURL = "http://" + u.Host
						result.Token = u.Query().Get("token")
						return
					}
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return gatepostLogsProcess{}, fmt.Errorf("timed out waiting for gatepost-logs URL")
	}
	if result.Token == "" {
		_ = cmd.Process.Kill()
		return gatepostLogsProcess{}, fmt.Errorf("gatepost-logs did not report an access token")
	}
	return result, nil
}

func gatepostLogsCommand(cfg GatepostRuntimeConfig, gatepostRoot, auditLog string, port int) (string, []string, string, error) {
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	if raw := getenvDefault("DEVX_GATEPOST_LOGS_CMD", cfg.LogsCommand); raw != "" {
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			return "", nil, "", fmt.Errorf("DEVX_GATEPOST_LOGS_CMD is empty")
		}
		return fields[0], append(fields[1:], "--audit", auditLog, "--listen", listen), "", nil
	}
	if gatepostRoot != "" {
		if _, err := os.Stat(filepath.Join(gatepostRoot, "cmd", "gatepost-logs")); err == nil {
			return "go", []string{"run", "./cmd/gatepost-logs", "--audit", auditLog, "--listen", listen}, gatepostRoot, nil
		}
	}
	return "", nil, "", fmt.Errorf("gatepost logs command not found; set trusted gatepost.logs_command/DEVX_GATEPOST_LOGS_CMD, or set gatepost.root/DEVX_GATEPOST_ROOT to a Gatepost checkout")
}

func stopGatepostLogs(pid int) {
	if pid <= 0 {
		return
	}
	p, err := os.FindProcess(pid)
	if err == nil {
		_ = p.Kill()
	}
}
