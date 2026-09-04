package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jfox85/devx/usage"
	"github.com/spf13/viper"
)

// UsageOptions configures the provider usage poller. It is viper-free so the
// desktop shell, which never initializes viper, can build it directly.
type UsageOptions struct {
	Enabled      bool
	PollInterval time.Duration
	RedlineURL   string
	TokenFile    string
	AllowRemote  bool
}

// UsageOptionsFromGlobalConfig builds UsageOptions the way every caller
// (cmd/web.go, cmd/web_windows.go, desktop/main.go) must: usage.enabled and
// usage.poll_interval may come from the merged viper singleton (a project
// override there is harmless), but the three usage.redline.* keys are
// security-sensitive — url, token_file and allow_remote control which host
// gets the Redline bearer token and which file on disk is read as that token.
// A project-level .devx/config.yaml is repo-supplied and must never set them:
// a malicious repo could point url at an attacker host and token_file at an
// arbitrary 0600 file to exfiltrate it. So the redline.* keys are read from a
// private viper instance pinned to the global config file only
// (~/.config/devx/config.yaml), mirroring the global-config path logic in
// initConfig (cmd/root.go), with the same DEVX_ env-var override support.
// Defaults for all four fields come from usage.DefaultBaseURL /
// usage.DefaultPollInterval, the same source cmd/root.go's viper defaults use.
func UsageOptionsFromGlobalConfig() UsageOptions {
	opts := UsageOptions{
		Enabled:      true,
		PollInterval: usage.DefaultPollInterval,
		RedlineURL:   usage.DefaultBaseURL,
	}
	if viper.IsSet("usage.enabled") {
		opts.Enabled = viper.GetBool("usage.enabled")
	}
	if d := viper.GetDuration("usage.poll_interval"); d > 0 {
		opts.PollInterval = d
	}

	global := globalOnlyViper()
	if url := global.GetString("usage.redline.url"); url != "" {
		opts.RedlineURL = url
	}
	opts.TokenFile = global.GetString("usage.redline.token_file")
	opts.AllowRemote = global.GetBool("usage.redline.allow_remote")
	return opts
}

// globalOnlyViper reads only ~/.config/devx/config.yaml (never a project-level
// .devx/config.yaml), with the same DEVX_ env-var override behavior as the
// main viper singleton (cmd/root.go's initConfig). A missing or unreadable
// global config file is not an error: the returned instance still answers env
// vars and, via the caller's defaulting, the documented defaults.
func globalOnlyViper() *viper.Viper {
	v := viper.New()
	home, err := os.UserHomeDir()
	if err == nil {
		v.SetConfigFile(filepath.Join(home, ".config", "devx", "config.yaml"))
		_ = v.ReadInConfig()
	}
	v.SetEnvPrefix("DEVX")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	return v
}

// ConfigureUsage validates opts and stores them for startBackground to build a
// poller from. It must be called before Start/Serve, and must not be called
// again while a poller built from a previous call is running (Shutdown or a
// never-started server): usage.Poller.Start panics if a single Poller instance
// is started twice, so reconfiguring live would either crash on the next start
// or silently keep serving the old config. Call ConfigureUsage again only
// after Shutdown (or before the first Start).
//
// Validation builds a Redline client from opts to catch a misconfigured URL
// (non-loopback without allow_remote, which would put the API token on the
// network) at startup rather than on the first poll; that client is discarded
// and startBackground builds its own so a fresh Start→Stop→Start cycle never
// reuses one. A missing or unreadable Redline token is not a startup failure:
// the poller still runs and reports "Redline rejected the API token" or
// "not running" instead of the server refusing to boot.
func (s *Server) ConfigureUsage(opts UsageOptions) error {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.usageCancel != nil {
		return fmt.Errorf("usage: cannot reconfigure while the poller is running; call Shutdown first")
	}
	if !opts.Enabled {
		s.usageOpts = nil
		s.usage = nil
		return nil
	}
	// The error already names the path it looked at; log it once here rather
	// than on every poll.
	token, _, err := usage.DiscoverToken(opts.TokenFile)
	if err != nil {
		log.Printf("usage: %v", err)
	}
	if _, err := usage.NewClient(opts.RedlineURL, token, opts.AllowRemote); err != nil {
		return err
	}
	optsCopy := opts
	s.usageOpts = &optsCopy
	return nil
}

// startBackground starts the server's background workers. Both Start and
// PrivateServer.Serve call it after registering routes. It builds a fresh
// usage.Client and usage.Poller every time it runs — never reusing the one
// from a previous start — because usage.Poller.Start panics if called twice on
// the same instance, which a Stop→Start cycle (e.g. desktop's Shutdown
// followed by a later Start) would otherwise hit.
func (s *Server) startBackground() {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.usageOpts == nil || s.usageCancel != nil {
		return
	}
	opts := *s.usageOpts
	token, _, err := usage.DiscoverToken(opts.TokenFile)
	if err != nil {
		log.Printf("usage: %v", err)
	}
	client, err := usage.NewClient(opts.RedlineURL, token, opts.AllowRemote)
	if err != nil {
		// ConfigureUsage already validated this URL; a failure here would mean the
		// stored opts were mutated some other way. Log and leave usage disabled for
		// this start rather than crash the server.
		log.Printf("usage: failed to build redline client at start: %v", err)
		return
	}
	poller := &usage.Poller{
		Client:   client,
		Interval: opts.PollInterval,
		Logger:   log.Default(),
		OnChange: s.broadcastUsage,
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.usage = poller
	s.usageCancel = cancel
	s.usageDone = poller.Start(ctx)
}

// cancelBackground signals the background workers to stop without waiting for
// them to exit. Split out from stopBackground so Shutdown can cancel the
// poller before shutting down the HTTP server, then wait afterward (see
// Shutdown's doc comment).
func (s *Server) cancelBackground() {
	s.bgMu.Lock()
	cancel := s.usageCancel
	s.bgMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitBackground waits for the background workers to exit, or until ctx
// expires, and clears their lifecycle handles so a later startBackground
// builds a fresh poller rather than reusing the stopped one.
func (s *Server) waitBackground(ctx context.Context) {
	s.bgMu.Lock()
	done := s.usageDone
	s.usageCancel, s.usageDone, s.usage = nil, nil, nil
	s.bgMu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// stopBackground cancels the background workers and waits for them to exit, or
// until ctx expires. Equivalent to cancelBackground followed by
// waitBackground; Shutdown calls the two phases separately so the HTTP
// server's own shutdown runs in between.
func (s *Server) stopBackground(ctx context.Context) {
	s.cancelBackground()
	s.waitBackground(ctx)
}

// currentPoller returns the poller currently backing GET /api/usage and the
// refresh endpoint, or nil when usage is disabled or not yet started.
func (s *Server) currentPoller() *usage.Poller {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	return s.usage
}

// usageEnabled reports whether provider usage is actually configured — i.e.
// whether ConfigureUsage was called with Enabled opts that passed validation
// — rather than what a viper key says. This is what handleSettings reports as
// usage_enabled.
func (s *Server) usageEnabled() bool {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	return s.usageOpts != nil
}

// broadcastUsage pushes a changed usage payload to connected SPA clients.
func (s *Server) broadcastUsage(u usage.Usage) {
	payload, err := json.Marshal(u)
	if err != nil {
		log.Printf("usage: failed to marshal broadcast payload: %v", err)
		return
	}
	s.hub.broadcastEvent("usage", string(payload))
}

// handleUsage serves the cached provider usage. It never contacts Redline, so
// it never blocks on a service that may not be running.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	poller := s.currentPoller()
	if poller == nil {
		writeJSON(w, http.StatusOK, usage.Disabled())
		return
	}
	writeJSON(w, http.StatusOK, poller.Current())
}

// handleUsageRefresh asks Redline for live provider usage. Partial provider
// failures still return the (re-read) usage so the strip updates; only a
// rejected token is reported as an error, since that needs operator action.
//
// Invariant: the response body is ALWAYS a usage.Usage, never a bare
// {"error":...} shape, so the SPA has exactly one parser for this endpoint’s
// body regardless of status code — disabled reports 404 with
// Usage{State: disabled}, a rejected token reports 502 with
// Usage{State: unavailable, Message: "Redline rejected the API token"}, and
// every other outcome (ok, throttled, cancelled) reports the current Usage
// with its own state already describing what happened.
func (s *Server) handleUsageRefresh(w http.ResponseWriter, r *http.Request) {
	poller := s.currentPoller()
	if poller == nil {
		writeJSON(w, http.StatusNotFound, usage.Disabled())
		return
	}
	result, err := poller.Refresh(r.Context())
	switch {
	case result.Throttled:
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(result.RetryAfter.Seconds()))))
		writeJSON(w, http.StatusAccepted, result.Usage)
	case errors.Is(err, usage.ErrUnauthorized):
		rejected := result.Usage
		rejected.State = usage.StateUnavailable
		rejected.Message = "Redline rejected the API token"
		writeJSON(w, http.StatusBadGateway, rejected)
	case errors.Is(err, context.Canceled):
		// The client went away mid-refresh; the refresh continues on the poller's
		// own context, so report the cached state rather than an error.
		writeJSON(w, http.StatusAccepted, result.Usage)
	default:
		if err != nil {
			log.Printf("usage: refresh reported errors: %v", err)
		}
		writeJSON(w, http.StatusOK, result.Usage)
	}
}
