package target

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
)

// GatepostTarget runs a DevX session behind a Gatepost runtime. The initial
// runtime is Docker + mitmproxy, but the persisted metadata is intentionally
// framed as a Gatepost capability rather than a mitmproxy-specific contract.
type GatepostTarget struct{}

type gatepostRuntime struct {
	base         string
	agentName    string
	proxyName    string
	internalNet  string
	egressNet    string
	portsNet     string
	sessionDir   string
	auditDir     string
	configDir    string
	agentHomeDir string
}

func (g *GatepostTarget) Type() string { return "gatepost" }

func newGatepostRuntime(sessionName string) (gatepostRuntime, error) {
	base := caddy.SanitizeHostname(sessionName)
	sessionDir, auditDir, configDir, agentHomeDir, err := gatepostDirs(sessionName)
	if err != nil {
		return gatepostRuntime{}, err
	}
	return gatepostRuntime{
		base:         base,
		agentName:    "devx-" + base,
		proxyName:    "devx-" + base + "-gatepost-proxy",
		internalNet:  "devx-" + base + "-gatepost-internal",
		egressNet:    "devx-" + base + "-gatepost-egress",
		portsNet:     "devx-" + base + "-gatepost-ports",
		sessionDir:   sessionDir,
		auditDir:     auditDir,
		configDir:    configDir,
		agentHomeDir: agentHomeDir,
	}, nil
}

func prepareGatepostStateDirs(r gatepostRuntime, policyPath string) error {
	if err := os.MkdirAll(r.auditDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(r.configDir, 0o700); err != nil {
		return err
	}
	for _, dir := range []string{
		r.agentHomeDir,
		filepath.Join(r.agentHomeDir, ".pi", "agent", "sessions"),
		filepath.Join(r.agentHomeDir, ".codex"),
		filepath.Join(r.agentHomeDir, ".claude"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		_ = os.Chmod(dir, 0o700)
	}
	if err := writeGatepostPolicy(policyPath); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.auditDir, "audit.jsonl"), nil, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.auditDir, "companion.jsonl"), nil, 0o600)
}

func recreateGatepostDockerShell(ctx context.Context, r gatepostRuntime) error {
	// Clean up stale containers from any previous failed attempt.
	_ = dockerRunIgnore(ctx, "rm", "-f", r.agentName)
	_ = dockerRunIgnore(ctx, "rm", "-f", r.proxyName)

	// Prune all Docker networks with no containers to free address pool space.
	// This prevents "all predefined address pools have been fully subnetted"
	// errors from accumulated stale networks across sessions.
	_ = dockerRunIgnore(ctx, "network", "prune", "-f")

	// Clean up any stale networks from a previous failed attempt before creating.
	_ = dockerRunIgnore(ctx, "network", "rm", r.internalNet)
	_ = dockerRunIgnore(ctx, "network", "rm", r.egressNet)
	_ = dockerRunIgnore(ctx, "network", "rm", r.portsNet)

	// Create all networks in parallel.
	// - internal: agent ↔ proxy only (no external access)
	// - egress: proxy external traffic
	// - ports: agent ↔ host for service port publishing (default bridge-style)
	type netErr struct {
		err  error
		name string
	}
	netCh := make(chan netErr, 3)
	go func() {
		netCh <- netErr{dockerRun(ctx, "network", "create", "--internal", r.internalNet), r.internalNet}
	}()
	go func() { netCh <- netErr{dockerRun(ctx, "network", "create", r.egressNet), r.egressNet} }()
	go func() { netCh <- netErr{dockerRun(ctx, "network", "create", r.portsNet), r.portsNet} }()
	var firstErr netErr
	for i := 0; i < 3; i++ {
		result := <-netCh
		if result.err != nil && firstErr.err == nil {
			firstErr = result
		}
	}
	if firstErr.err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = dockerRunIgnore(cleanupCtx, "network", "rm", r.internalNet)
		_ = dockerRunIgnore(cleanupCtx, "network", "rm", r.egressNet)
		_ = dockerRunIgnore(cleanupCtx, "network", "rm", r.portsNet)
		return fmt.Errorf("create gatepost network %s: %w", firstErr.name, firstErr.err)
	}
	return nil
}

func (g *GatepostTarget) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
	// Apply a hard timeout to prevent indefinite hangs on Docker operations.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	runtime, err := newGatepostRuntime(opts.SessionName)
	if err != nil {
		return nil, err
	}
	gatepostCfg := opts.GatepostConfig
	// gatepost.root is a trusted config value. Do not use env root overrides for
	// executable helper discovery; workspace shells can influence env.
	gatepostRoot := gatepostCfg.Root
	trustedAdaptersDir, err := gatepostAdaptersDir(gatepostRoot)
	if err != nil {
		return nil, err
	}
	policyPath := filepath.Join(runtime.configDir, "policy.gatepost.yaml")
	statePrepared := false
	success := false
	defer func() {
		if statePrepared && !success {
			_ = removeGatepostRuntimeState(runtime)
		}
	}()
	if err := prepareGatepostStateDirs(runtime, policyPath); err != nil {
		return nil, err
	}
	statePrepared = true
	if err := writeGatepostAgentHookConfigs(runtime, trustedAdaptersDir); err != nil {
		return nil, err
	}

	controlPort, err := freePort()
	if err != nil {
		return nil, err
	}
	logsPort, err := freePort()
	if err != nil {
		return nil, err
	}
	controlToken := randomHex(24)
	eventToken := randomHex(24)

	// Persist control token so secrets can be re-registered after proxy restarts.
	if err := os.WriteFile(filepath.Join(runtime.configDir, "control.token"), []byte(controlToken), 0o600); err != nil {
		return nil, fmt.Errorf("write control token: %w", err)
	}

	fmt.Println("Preparing Gatepost environment...")
	if err := recreateGatepostDockerShell(ctx, runtime); err != nil {
		return nil, err
	}

	proxyImage := getenvDefault("DEVX_GATEPOST_PROXY_IMAGE", "gatepost-proxy:latest")
	agentImage := opts.Image
	if agentImage == "" {
		agentImage = getenvDefault("DEVX_GATEPOST_AGENT_IMAGE", getenvDefault("DEVX_DOCKER_IMAGE", "gatepost-pi-agent:latest"))
	}
	proxyArgs := []string{"run", "-d", "--name", runtime.proxyName,
		"--network", runtime.egressNet, "--network-alias", "gatepost-control",
		"--restart", "unless-stopped",
		"-p", fmt.Sprintf("127.0.0.1:%d:18082", controlPort),
		"-v", runtime.auditDir + ":/audit",
		"-v", runtime.configDir + ":/config:ro",
		"-v", opts.WorktreePath + ":/workspace:ro",
		"-e", "GATEPOST_SESSION_ID=" + opts.SessionName,
		"-e", "GATEPOST_AUDIT_DIR=/audit",
		"-e", "GATEPOST_CONFIG_DIR=/config",
		"-e", "GATEPOST_POLICY_FILE=/config/policy.gatepost.yaml",
		"-e", "GATEPOST_PHASE=run",
		"-e", "GATEPOST_CONTROL_ADDR=gatepost-control:18082",
		"-e", "GATEPOST_CONTROL_TOKEN=" + controlToken,
		"-e", "GATEPOST_EVENTS_ADDR=0.0.0.0:9100",
		"-e", "GATEPOST_EVENTS_TOKEN=" + eventToken,
		"-e", "GATEPOST_SMART_ENABLED=true",
		"-e", "GATEPOST_SMART_PROVIDER=" + getenvDefault("DEVX_GATEPOST_SMART_PROVIDER", "cursor"),
		"-e", "GATEPOST_SMART_MODEL=" + getenvDefault("DEVX_GATEPOST_SMART_MODEL", "claude-4.6-sonnet-medium"),
		"-e", "GATEPOST_SMART_BASE_URL=" + getenvDefault("DEVX_GATEPOST_SMART_BASE_URL", "http://host.docker.internal:8318/v1"),
		"-e", "GATEPOST_SMART_ALLOWED_HOSTS=" + getenvDefault("DEVX_GATEPOST_SMART_ALLOWED_HOSTS", "host.docker.internal"),
		"-e", "GATEPOST_LLM_API_KEY=" + getenvDefault("GATEPOST_LLM_API_KEY", os.Getenv("CLIPROXYAPI_API_KEY")),
		"-e", "GATEPOST_BLOCK_PROXY_HOSTS=gatepost-control",
		"-e", "GATEPOST_BLOCK_PROXY_PORTS=18082",
		"-e", "GATEPOST_HOST_INTERNAL_ALLOWED_PORTS=" + getenvDefault("DEVX_GATEPOST_HOST_INTERNAL_ALLOWED_PORTS", "8317,8318"),
	}
	for k, v := range opts.Labels {
		proxyArgs = append(proxyArgs, "--label", k+"="+v)
	}
	proxyArgs = append(proxyArgs, "--label", "devx.role=gatepost-proxy", proxyImage)
	fmt.Println("Starting Gatepost proxy...")
	cleanupRuntime := func(meta session.TargetMeta) error {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		return g.cleanupStrict(cleanupCtx, meta)
	}
	if err := dockerRun(ctx, proxyArgs...); err != nil {
		if cleanupErr := cleanupRuntime(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("create gatepost proxy: %w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, fmt.Errorf("create gatepost proxy: %w", err)
	}
	if err := dockerRun(ctx, "network", "connect", "--alias", "proxy", "--alias", "gatepost-events", runtime.internalNet, runtime.proxyName); err != nil {
		if cleanupErr := cleanupRuntime(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("connect proxy to internal network: %w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, fmt.Errorf("connect proxy to internal network: %w", err)
	}
	controlURL := fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	// Wait for proxy health and start gatepost-logs in parallel.
	type logsResult struct {
		proc gatepostLogsProcess
		err  error
	}
	logsCtx, logsCancel := context.WithCancel(context.Background())
	logsCh := make(chan logsResult, 1)
	go func() {
		proc, err := startGatepostLogs(logsCtx, gatepostCfg, gatepostRoot, filepath.Join(runtime.auditDir, "audit.jsonl"), logsPort)
		logsCh <- logsResult{proc, err}
	}()
	cleanupWithLogs := func(meta session.TargetMeta) error {
		logsCancel()
		if r, ok := <-logsCh; ok && r.proc.PID > 0 {
			meta.Gatepost.LogsPID = r.proc.PID
		}
		return cleanupRuntime(meta)
	}
	if err := waitHTTP(ctx, controlURL+"/healthz", 30*time.Second); err != nil {
		if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("gatepost control did not become healthy: %w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, fmt.Errorf("gatepost control did not become healthy: %w", err)
	}
	fmt.Println("Registering provider secrets...")
	providerBootstrap, err := bootstrapGatepostProviderSecrets(gatepostCfg, gatepostRoot, controlURL, controlToken)
	if err != nil {
		if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, err
	}

	// Start agent on the ports network (normal bridge) so -p port publishing works,
	// then connect to the internal network for proxy/events access.
	// Mount the main repo .git dir at the same host path so the container can
	// resolve worktree references. Then overlay /workspace/.git with a file
	// that points to a container-local path (/root/.git-worktree) instead of
	// the host path, so tools like `git rev-parse --git-dir` report container
	// paths (not /Users/jfox/...). The bootstrap step copies worktree metadata
	// to /root/.git-worktree and sets commondir to the mounted main .git.
	mainGitDir := mainRepoGitDir(opts.WorktreePath)

	// Write the container-local .git file to the session dir; it will be
	// mounted over /workspace/.git so the host's actual .git is untouched.
	var containerGitFile string
	if mainGitDir != "" {
		containerGitFile = filepath.Join(runtime.sessionDir, "container-dot-git")
		if err := os.WriteFile(containerGitFile, []byte("gitdir: /root/.git-worktree\n"), 0o644); err != nil {
			if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
				return nil, fmt.Errorf("write container .git file: %w; cleanup failed: %v", err, cleanupErr)
			}
			return nil, fmt.Errorf("write container .git file: %w", err)
		}
	}

	agentArgs := []string{"run", "-d", "--name", runtime.agentName,
		"--network", runtime.portsNet,
		"--restart", "unless-stopped",
		"-v", opts.WorktreePath + ":/workspace",
		"-v", filepath.Join(runtime.agentHomeDir, ".pi", "agent", "sessions") + ":/root/.pi/agent/sessions",
		"-v", filepath.Join(runtime.agentHomeDir, ".codex") + ":/root/.codex",
		"-v", filepath.Join(runtime.agentHomeDir, ".claude") + ":/root/.claude",
		"-w", "/workspace",
	}
	if mainGitDir != "" {
		// Mount read-write so git commit/add/push work inside the container.
		agentArgs = append(agentArgs, "-v", mainGitDir+":"+mainGitDir)
		// Overlay /workspace/.git with the container-local version.
		agentArgs = append(agentArgs, "-v", containerGitFile+":/workspace/.git")
	}
	for _, port := range opts.HostPorts {
		agentArgs = append(agentArgs, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port))
	}
	if models := piModelsFile(); models != "" {
		agentArgs = append(agentArgs, "-v", models+":/root/.pi/agent/models.json")
	}
	if trustedAdaptersDir != "" {
		agentArgs = append(agentArgs, "-v", trustedAdaptersDir+":/opt/gatepost/adapters:ro")
	}
	// Bind-mount sessions.json read-only so `devx artifact` can resolve sessions inside the container.
	sessionsPath := config.GetSessionsPath()
	if sessionsPath != "" {
		agentArgs = append(agentArgs, "-v", sessionsPath+":/root/.config/devx/sessions.json:ro")
	}
	// Per-session uploads directory — files uploaded via the web UI are saved
	// here on the host and mounted read-only into the container so the agent
	// can reference them (e.g. screenshots pasted into the terminal).
	homeD, _ := os.UserHomeDir()
	uploadsDir := filepath.Join(homeD, ".devx", "uploads", opts.SessionName)
	_ = os.MkdirAll(uploadsDir, 0o700)
	agentArgs = append(agentArgs, "-v", uploadsDir+":/root/.devx/uploads:ro")
	if cfg := piConfigDir(); cfg != "" {
		if _, err := os.Stat(cfg); err == nil {
			agentArgs = append(agentArgs, "-v", cfg+":/pi-config:ro")
			for _, item := range []string{"settings.json", "AGENTS.md", "skills", "prompts", "agents"} {
				hostPath := filepath.Join(cfg, item)
				if _, err := os.Stat(hostPath); err == nil {
					agentArgs = append(agentArgs, "-v", hostPath+":/root/.pi/agent/"+item+":ro")
				}
			}
			// Mount agents from ~/.pi/agent/agents/ if not found in pi-config dir.
			if _, err := os.Stat(filepath.Join(cfg, "agents")); os.IsNotExist(err) {
				homeD, _ := os.UserHomeDir()
				piAgents := filepath.Join(homeD, ".pi", "agent", "agents")
				if _, err := os.Stat(piAgents); err == nil {
					agentArgs = append(agentArgs, "-v", piAgents+":/root/.pi/agent/agents:ro")
				}
			}
		}
	}
	sec := opts.Security
	if sec.MemoryLimit == "" {
		sec = DefaultSecurityOpts()
	}
	for _, cap := range sec.CapDrop {
		agentArgs = append(agentArgs, "--cap-drop="+cap)
	}
	if sec.NoNewPrivs {
		agentArgs = append(agentArgs, "--security-opt", "no-new-privileges")
	}
	if sec.MemoryLimit != "" {
		agentArgs = append(agentArgs, "--memory", sec.MemoryLimit)
	}
	if sec.CPULimit != "" {
		agentArgs = append(agentArgs, "--cpus", sec.CPULimit)
	}
	if sec.PidsLimit > 0 {
		agentArgs = append(agentArgs, "--pids-limit", fmt.Sprintf("%d", sec.PidsLimit))
	}
	projectAlias := opts.Labels["devx.project"]
	agentRole := "devx_" + sanitizeRoleSegment(projectAlias) + "_coding_session"
	agentBranch := opts.SessionName

	agentEnv := map[string]string{
		"HTTP_PROXY": "http://proxy:8080", "HTTPS_PROXY": "http://proxy:8080", "http_proxy": "http://proxy:8080", "https_proxy": "http://proxy:8080",
		"NO_PROXY": "localhost,127.0.0.1,gatepost-events", "no_proxy": "localhost,127.0.0.1,gatepost-events",
		"GATEPOST_EVENTS_URL": "http://gatepost-events:9100/v1/events", "GATEPOST_EVENTS_TOKEN": eventToken,
		"GATEPOST_AGENT_ROLE": agentRole, "GATEPOST_AGENT_BRANCH": agentBranch,
		"CLIPROXYAPI_API_KEY": "GATEPOST_SECRET:cliproxy-key", "ANTHROPIC_API_KEY": "sk-ant-oat01-GATEPOST_SECRET:anthropic-oauth", "OPENAI_API_KEY": "GATEPOST_SECRET:openai-key", "GEMINI_API_KEY": "GATEPOST_SECRET:gemini-key",
		"CODEX_HOME": "/root/.codex", "CODEX_DISABLE_UPDATE_CHECK": "1", "DISABLE_AUTOUPDATER": "1",
		// DevX artifact support inside the container.
		"SESSION_NAME": opts.SessionName, "DEVX_SESSION_PATH": "/workspace",
		// Prevent Go from auto-downloading toolchains through the proxy (large zips can fail).
		"GOTOOLCHAIN": "local",
		// Vite dev servers must bind to 0.0.0.0 for Docker port publishing to work.
		"VITE_DEV_REMOTE": "1",
		// GitHub auth via proxy secret injection — placeholder, real token never enters the container.
		"GH_TOKEN": "GATEPOST_SECRET:gh-token",
	}
	if extDomain, ok := config.GetConfigValue("external_domain").(string); ok && extDomain != "" {
		// Allow Vite to accept requests from cloudflare tunnel hostnames.
		agentEnv["VITE_ALLOWED_HOSTS"] = "." + extDomain
	}
	for k, v := range providerBootstrap.Env {
		agentEnv[k] = v
	}
	for k, v := range opts.Env {
		agentEnv[k] = v
	}
	for k, v := range agentEnv {
		agentArgs = append(agentArgs, "-e", k+"="+v)
	}
	for k, v := range opts.Labels {
		agentArgs = append(agentArgs, "--label", k+"="+v)
	}
	agentArgs = append(agentArgs, "--label", "devx.role=agent", agentImage, "sleep", "infinity")
	fmt.Println("Starting agent container...")
	if err := dockerRun(ctx, agentArgs...); err != nil {
		if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("create gatepost agent: %w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, fmt.Errorf("create gatepost agent: %w", err)
	}
	// Connect agent to the internal network so it can reach the proxy/events service.
	if err := dockerRun(ctx, "network", "connect", runtime.internalNet, runtime.agentName); err != nil {
		if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("connect agent to internal network: %w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, fmt.Errorf("connect agent to internal network: %w", err)
	}
	if cfg := piConfigDir(); cfg != "" {
		if _, err := os.Stat(filepath.Join(cfg, "extensions", "package.json")); err == nil {
			// Copy extension .ts files from pi-config. Skip npm install for packages
			// that have pre-baked node_modules in the image; only install if a
			// package.json has no corresponding node_modules (new dep added).
			bootstrapScript := strings.Join([]string{
				"cp -r /pi-config/extensions/. /root/.pi/agent/extensions/",
				// Remove extensions that require host-only resources (Android SDK,
				// host tmux, Basic Memory database) and cannot function in a container.
				"rm -rf /root/.pi/agent/extensions/basic-memory-work /root/.pi/agent/extensions/mobile-tools.ts /root/.pi/agent/extensions/clone-pane.ts",
				"cd /root/.pi/agent/extensions",
				"[ ! -d node_modules ] && npm install --prefer-offline --no-audit --no-fund --silent >/dev/null 2>&1 || true",
				"for subpkg in /root/.pi/agent/extensions/*/package.json; do",
				`  dir=$(dirname "$subpkg")`,
				`  [ ! -d "$dir/node_modules" ] && (cd "$dir" && npm install --prefer-offline --no-audit --no-fund --silent >/dev/null 2>&1) || true`,
				"done",
				"true",
			}, "\n")
			if err := dockerRun(ctx, "exec", runtime.agentName, "bash", "-lc", bootstrapScript); err != nil {
				if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
					return nil, fmt.Errorf("bootstrap pi extensions: %w; cleanup failed: %v", err, cleanupErr)
				}
				return nil, fmt.Errorf("bootstrap pi extensions: %w", err)
			}
		}
	}
	// Set up GitHub auth via proxy secret injection (no plaintext token in container).
	// gh CLI uses GH_TOKEN env var; proxy replaces the placeholder with the real token.
	// Also convert SSH remote to HTTPS since SSH isn't available in containers.
	{
		ghSetup := strings.Join([]string{
			// Write minimal gh config so gh believes it's authenticated.
			`mkdir -p /root/.config/gh`,
			`printf 'github.com:\n  git_protocol: https\n  oauth_token: GATEPOST_SECRET:gh-token\n' > /root/.config/gh/hosts.yml`,
			// Configure git to use gh as credential helper for HTTPS operations.
			`gh auth setup-git 2>/dev/null`,
			// Convert SSH remote to HTTPS.
			`cd /workspace && git remote get-url origin 2>/dev/null | grep -q 'git@github.com:' && git remote set-url origin "$(git remote get-url origin | sed 's|git@github.com:|https://github.com/|')" 2>/dev/null`,
			`true`,
		}, " && ")
		_ = dockerRun(ctx, "exec", runtime.agentName, "bash", "-lc", ghSetup)
	}
	// Copy worktree metadata to /root/.git-worktree so git uses container-local
	// paths. The /workspace/.git file already points here via the overlay mount.
	if mainGitDir != "" {
		gitFileData, _ := os.ReadFile(filepath.Join(opts.WorktreePath, ".git"))
		hostGitDir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(gitFileData)), "gitdir: "))
		if hostGitDir != "" {
			gitRewrite := strings.Join([]string{
				`mkdir -p /root/.git-worktree`,
				fmt.Sprintf(`cp -r %q/. /root/.git-worktree/`, hostGitDir),
				fmt.Sprintf(`echo %q > /root/.git-worktree/commondir`, mainGitDir),
				`echo /workspace > /root/.git-worktree/gitdir`,
				`true`,
			}, " && ")
			_ = dockerRun(ctx, "exec", runtime.agentName, "bash", "-lc", gitRewrite)
		}
	}

	containerID, err := dockerOutput(ctx, "inspect", "--format", "{{.Id}}", runtime.agentName)
	if err != nil {
		if cleanupErr := cleanupWithLogs(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, err
	}
	// Collect the logs process that was started in parallel earlier.
	logsCancel() // no longer need cancellation — we're collecting the result
	logsRes := <-logsCh
	if logsRes.err != nil {
		if cleanupErr := cleanupRuntime(gatepostCleanupMeta(runtime, 0)); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup failed: %v", logsRes.err, cleanupErr)
		}
		return nil, logsRes.err
	}
	logs := logsRes.proc
	logsTokenPath := filepath.Join(runtime.sessionDir, "logs.token")
	if err := os.WriteFile(logsTokenPath, []byte(logs.Token), 0o600); err != nil {
		if cleanupErr := cleanupRuntime(gatepostCleanupMeta(runtime, logs.PID)); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup failed: %v", err, cleanupErr)
		}
		return nil, err
	}
	success = true
	return &StartResult{Meta: session.TargetMeta{Type: "gatepost", ContainerID: containerID, ContainerName: runtime.agentName, NetworkName: runtime.internalNet, Image: agentImage, Gatepost: session.GatepostMeta{Enabled: true, Runtime: "docker-mitmproxy", ProxyContainerName: runtime.proxyName, InternalNetworkName: runtime.internalNet, EgressNetworkName: runtime.egressNet, PortsNetworkName: runtime.portsNet, SessionDir: runtime.sessionDir, AuditDir: runtime.auditDir, ConfigDir: runtime.configDir, AgentHomeDir: runtime.agentHomeDir, AuditLog: filepath.Join(runtime.auditDir, "audit.jsonl"), CompanionLog: filepath.Join(runtime.auditDir, "companion.jsonl"), ControlURL: controlURL, LogsURL: logs.PublicURL, LogsTokenPath: logsTokenPath, LogsPID: logs.PID, ProviderMode: providerBootstrap.Mode, ProviderCommand: providerBootstrap.Command, RegisteredProviders: providerBootstrap.Registered, ProviderWarnings: providerBootstrap.Warnings}}}, nil
}

func (g *GatepostTarget) Stop(ctx context.Context, meta session.TargetMeta) error {
	stopGatepostLogs(meta.Gatepost.LogsPID)
	return g.cleanupStrict(ctx, meta)
}

func (g *GatepostTarget) cleanup(ctx context.Context, meta session.TargetMeta) error {
	_ = g.cleanupWithRunner(ctx, meta, func(args ...string) error { return dockerRunIgnore(ctx, args...) })
	return nil
}

func (g *GatepostTarget) cleanupStrict(ctx context.Context, meta session.TargetMeta) error {
	return g.cleanupWithRunner(ctx, meta, func(args ...string) error { return dockerRun(ctx, args...) })
}

func (g *GatepostTarget) cleanupWithRunner(ctx context.Context, meta session.TargetMeta, run func(args ...string) error) error {
	stopGatepostLogs(meta.Gatepost.LogsPID)
	var errs []string
	cleanup := func(args ...string) {
		if err := run(args...); err != nil && !isDockerAlreadyGone(err) {
			errs = append(errs, fmt.Sprintf("docker %s: %v", strings.Join(args, " "), err))
		}
	}
	if meta.ContainerName != "" {
		cleanup("rm", "-f", meta.ContainerName)
	}
	if meta.Gatepost.ProxyContainerName != "" {
		cleanup("rm", "-f", meta.Gatepost.ProxyContainerName)
	}
	if meta.NetworkName != "" {
		cleanup("network", "rm", meta.NetworkName)
	}
	if meta.Gatepost.EgressNetworkName != "" {
		cleanup("network", "rm", meta.Gatepost.EgressNetworkName)
	}
	if meta.Gatepost.PortsNetworkName != "" {
		cleanup("network", "rm", meta.Gatepost.PortsNetworkName)
	}
	if len(errs) > 0 {
		return fmt.Errorf("gatepost runtime cleanup failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func isDockerAlreadyGone(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such") || strings.Contains(msg, "not found")
}

func gatepostCleanupMeta(r gatepostRuntime, logsPID int) session.TargetMeta {
	return session.TargetMeta{
		ContainerName: r.agentName,
		NetworkName:   r.internalNet,
		Gatepost: session.GatepostMeta{
			ProxyContainerName: r.proxyName,
			EgressNetworkName:  r.egressNet,
			PortsNetworkName:   r.portsNet,
			LogsPID:            logsPID,
		},
	}
}

// mainRepoGitDir returns the main repo's .git directory given a worktree path,
// by reading the worktree's .git file (which contains "gitdir: /path/to/.git/worktrees/<name>").
// Returns empty string if the worktree .git file can't be resolved.
func mainRepoGitDir(worktreePath string) string {
	gitFile := filepath.Join(worktreePath, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	// Format: "gitdir: /abs/path/to/.git/worktrees/<name>"
	line := strings.TrimSpace(string(data))
	gitdirPath, ok := strings.CutPrefix(line, "gitdir: ")
	if !ok {
		return ""
	}
	// Walk up from .git/worktrees/<name> to find the main .git dir
	// .git/worktrees/<name> -> .git/worktrees -> .git
	mainGit := filepath.Dir(filepath.Dir(gitdirPath))
	if info, err := os.Stat(mainGit); err != nil || !info.IsDir() {
		return ""
	}
	return mainGit
}

func gatepostAdaptersDir(gatepostRoot string) (string, error) {
	if gatepostRoot == "" {
		return "", nil
	}
	resolvedRoot, err := filepath.EvalSymlinks(gatepostRoot)
	if err != nil {
		return "", fmt.Errorf("trusted gatepost root unavailable %s: %w", gatepostRoot, err)
	}
	if err := validateTrustedGatepostPath(resolvedRoot, true); err != nil {
		return "", err
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, "adapters"))
	if err != nil {
		return "", fmt.Errorf("gatepost adapters unavailable under trusted root %s: %w", gatepostRoot, err)
	}
	if err := validateTrustedGatepostChild(resolvedRoot, resolvedDir, true); err != nil {
		return "", err
	}
	for _, tool := range []string{"claude", "codex"} {
		toolDir, err := filepath.EvalSymlinks(filepath.Join(resolvedDir, tool))
		if err != nil {
			return "", fmt.Errorf("gatepost adapter dir %s unavailable: %w", tool, err)
		}
		if err := validateTrustedGatepostChild(resolvedRoot, toolDir, true); err != nil {
			return "", err
		}
		path, err := filepath.EvalSymlinks(filepath.Join(toolDir, "gatepost-events.py"))
		if err != nil {
			return "", fmt.Errorf("gatepost adapter %s unavailable: %w", filepath.Join(tool, "gatepost-events.py"), err)
		}
		if err := validateTrustedGatepostChild(resolvedRoot, path, false); err != nil {
			return "", err
		}
	}
	return resolvedDir, nil
}

func validateTrustedGatepostChild(root, path string, wantDir bool) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("trusted Gatepost path escapes trusted root %s: %s", root, path)
	}
	return validateTrustedGatepostPath(path, wantDir)
}

func validateTrustedGatepostPath(path string, wantDir bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if wantDir && !info.IsDir() {
		return fmt.Errorf("trusted Gatepost path is not a directory: %s", path)
	}
	if !wantDir && info.IsDir() {
		return fmt.Errorf("trusted Gatepost adapter is a directory: %s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("trusted Gatepost path is group/world-writable: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("could not inspect owner for trusted Gatepost path: %s", path)
	}
	if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("trusted Gatepost path owner uid %d does not match current uid %d: %s", stat.Uid, os.Getuid(), path)
	}
	return nil
}

func removeGatepostRuntimeState(r gatepostRuntime) error {
	if r.sessionDir == "" {
		return nil
	}
	return os.RemoveAll(r.sessionDir)
}

type gatepostHookEvent struct {
	Name    string
	Matcher string
}

func writeGatepostAgentHookConfigs(r gatepostRuntime, adaptersDir string) error {
	if adaptersDir == "" {
		return nil
	}
	if err := writeHookConfig(
		filepath.Join(r.agentHomeDir, ".claude", "settings.json"),
		"python3 /opt/gatepost/adapters/claude/gatepost-events.py",
		[]gatepostHookEvent{
			{"SessionStart", "startup|resume|clear|compact"}, {"UserPromptSubmit", ""},
			{"PreToolUse", "*"}, {"PostToolUse", "*"}, {"PostToolUseFailure", "*"},
			{"PermissionRequest", "*"}, {"PermissionDenied", "*"}, {"PostToolBatch", ""},
			{"Stop", ""}, {"StopFailure", "*"}, {"SessionEnd", "*"},
			{"PreCompact", "manual|auto"}, {"PostCompact", "manual|auto"},
			{"SubagentStart", "*"}, {"SubagentStop", "*"}, {"Notification", "*"},
			{"CwdChanged", ""}, {"InstructionsLoaded", "*"},
		},
	); err != nil {
		return err
	}
	return writeHookConfig(
		filepath.Join(r.agentHomeDir, ".codex", "hooks.json"),
		"python3 /opt/gatepost/adapters/codex/gatepost-events.py",
		[]gatepostHookEvent{
			{"SessionStart", "startup|resume|clear|compact"}, {"UserPromptSubmit", ""},
			{"PreToolUse", "*"}, {"PostToolUse", "*"}, {"PermissionRequest", "*"},
			{"Stop", ""}, {"PreCompact", "manual|auto"}, {"PostCompact", "manual|auto"},
			{"SubagentStart", "*"}, {"SubagentStop", "*"},
		},
	)
}

func writeHookConfig(path, command string, events []gatepostHookEvent) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite symlink hook config: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	type hookEntry struct {
		Name          string `json:"name"`
		Type          string `json:"type"`
		Command       string `json:"command"`
		Timeout       int    `json:"timeout"`
		StatusMessage string `json:"statusMessage"`
	}
	type hookGroup struct {
		Matcher string      `json:"matcher,omitempty"`
		Hooks   []hookEntry `json:"hooks"`
	}
	data := struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}{Hooks: map[string][]hookGroup{}}
	for _, event := range events {
		group := hookGroup{
			Matcher: event.Matcher,
			Hooks: []hookEntry{{
				Name:          "gatepost-events",
				Type:          "command",
				Command:       command,
				Timeout:       10,
				StatusMessage: "Gatepost event capture",
			}},
		}
		data.Hooks[event.Name] = append(data.Hooks[event.Name], group)
	}
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func gatepostDirs(sessionName string) (string, string, string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", "", err
	}
	base := filepath.Join(home, ".local", "share", "devx", "gatepost", caddy.SanitizeHostname(sessionName))
	return base, filepath.Join(base, "audit"), filepath.Join(base, "config"), filepath.Join(base, "agent-home"), nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitHTTP(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func getenvDefault(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}

// sanitizeRoleSegment makes a string safe for use in a role name:
// lowercase, non-alphanumeric runs become single underscores, leading/trailing underscores stripped.
func sanitizeRoleSegment(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevUnderscore := true // suppress leading underscores
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	result := strings.TrimRight(b.String(), "_")
	if result == "" {
		return "unknown"
	}
	return result
}

// ReprovisionGatepostSecrets checks if the proxy lost secrets (e.g. after a
// container restart) and re-runs provider bootstrap to restore them.
// cfg supplies trusted runtime config (logs command, root, etc.).
func ReprovisionGatepostSecrets(gp session.GatepostMeta, cfg GatepostRuntimeConfig) {
	if gp.ControlURL == "" || gp.ConfigDir == "" {
		return
	}
	tokenBytes, err := os.ReadFile(filepath.Join(gp.ConfigDir, "control.token"))
	if err != nil {
		return
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return
	}
	// Check if secrets are already present.
	req, _ := http.NewRequest(http.MethodGet, gp.ControlURL+"/secrets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var body struct {
		Secrets []string `json:"secrets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return
	}
	if len(body.Secrets) > 0 {
		return // secrets present, nothing to do
	}
	// Re-run provider bootstrap from the trusted configured Gatepost checkout.
	log.Printf("gatepost reprovision: secrets empty for %s, re-bootstrapping", gp.ControlURL)
	if _, err := bootstrapGatepostProviderSecrets(cfg, cfg.Root, gp.ControlURL, token); err != nil {
		log.Printf("gatepost reprovision: %v", err)
	} else {
		log.Printf("gatepost reprovision: secrets restored")
	}
}
