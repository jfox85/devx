package usage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// ErrUnreachable reports that the Redline service could not be contacted, which
// normally means it is not running on this host.
var ErrUnreachable = errors.New("redline unreachable")

// ErrUnauthorized reports that Redline rejected the API token.
var ErrUnauthorized = errors.New("redline rejected the API token")

// ErrTimeout reports that Redline was reachable but did not answer within the
// call's deadline, which is a different diagnosis from it not running.
var ErrTimeout = errors.New("redline did not respond in time")

const (
	// DashboardTimeout bounds a dashboard read when the caller's context carries
	// no deadline; the read is a DB-only query so it should never come close.
	DashboardTimeout = 5 * time.Second
	// RefreshTimeout bounds one provider refresh when the caller's context
	// carries no deadline. Redline allows 10s per upstream fetch.
	RefreshTimeout = 12 * time.Second
)

// DefaultBaseURL is where Redline listens by default.
const DefaultBaseURL = "http://127.0.0.1:7436"

// maxBodyBytes caps how much of a Redline response we read.
const maxBodyBytes = 2 << 20 // 2 MiB

// maxDrainBytes caps how much of an ignored response body we read back to make
// the connection reusable; past this it is cheaper to drop the connection.
const maxDrainBytes = 4 << 10 // 4 KiB

// providerIDPattern is the shape Redline gives its provider account ids. The
// id goes into a request path, so anything outside this alphabet is refused
// before a URL is built rather than escaped and sent.
var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Client talks to a Redline service over its loopback HTTP API. Prefer NewClient,
// which validates the base URL before any token is put on the wire.
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewClient validates baseURL and returns a Client for it. An empty baseURL
// means DefaultBaseURL. Non-loopback hosts are refused unless allowRemote is
// set, so a mistyped URL cannot ship the Redline bearer token elsewhere, and an
// allowed remote host must speak https so the token is never sent in the clear.
// The returned BaseURL keeps only the scheme and host.
func NewClient(baseURL, token string, allowRemote bool) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse redline url %q: %w", baseURL, err)
	}
	// Every message below reports the redacted URL: a mistyped base URL may carry
	// a password in its userinfo and errors end up in logs.
	shown := parsed.Redacted()
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("redline url %q: scheme must be http or https", shown)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("redline url %q: missing host", shown)
	}
	if parsed.User != nil {
		// Redline authenticates with a bearer token; embedded credentials would
		// only be a second secret going onto the wire by accident.
		return nil, fmt.Errorf("redline url %q: must not carry credentials", shown)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("redline url %q: must not carry a query or fragment", shown)
	}
	if !isLoopbackHost(parsed.Hostname()) {
		if !allowRemote {
			return nil, fmt.Errorf("redline url %q is not loopback; set allow_remote to use it", shown)
		}
		if parsed.Scheme != "https" {
			// Off-host means the bearer token crosses a network: refuse to put it
			// on the wire in the clear.
			return nil, fmt.Errorf("redline url %q is not loopback; a remote redline must use https", shown)
		}
	}
	// Keep scheme and host only: request paths are built from the API's own
	// constants, so a path here could only misdirect them.
	base := url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	return &Client{BaseURL: base.String(), Token: token}, nil
}

// isLoopbackHost reports whether host is 127.0.0.0/8, ::1 or "localhost".
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// defaultHTTPClient is the package's own client so Redline calls never inherit
// http.DefaultClient's transport, which any other package can reconfigure.
// Redirects are refused outright: following one would resend the Redline bearer
// token to whatever host the Location names. Proxy is nil so an HTTP_PROXY in
// the environment cannot intercept a loopback call.
//
// No Client.Timeout: each call applies its own context deadline so a slow
// refresh is not cut short by the dashboard budget.
var defaultHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		Proxy:                 nil,
		MaxIdleConnsPerHost:   4,
		ResponseHeaderTimeout: 15 * time.Second,
	},
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return defaultHTTPClient
}

// newRequest builds an authenticated request against the Redline base URL.
func (c *Client) newRequest(ctx context.Context, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build redline request for %s: %w", path, err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// withTimeout applies fallback as a deadline when ctx has none of its own.
func withTimeout(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, fallback)
}

// transportError classifies a failed round trip: a deadline or network timeout
// means Redline is slow, anything else means we could not reach it at all.
func transportError(err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return fmt.Errorf("%w: %v", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %v", ErrUnreachable, err)
}

// Dashboard returns the raw body of Redline's GET /v1/dashboard. When ctx has
// no deadline, DashboardTimeout applies.
func (c *Client) Dashboard(ctx context.Context) ([]byte, error) {
	ctx, cancel := withTimeout(ctx, DashboardTimeout)
	defer cancel()

	req, err := c.newRequest(ctx, http.MethodGet, "/v1/dashboard")
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, transportError(err)
	}
	defer resp.Body.Close()

	if err := statusError(resp); err != nil {
		return nil, err
	}

	// One byte past the cap distinguishes "exactly at the cap" from "too large",
	// so an oversized body is reported as such instead of failing as bad JSON.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, transportError(err)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("redline dashboard response too large (over %d bytes)", maxBodyBytes)
	}
	return body, nil
}

// Refresh asks Redline to fetch live usage for one provider account id, for
// example "claude-main". When ctx has no deadline, RefreshTimeout applies.
func (c *Client) Refresh(ctx context.Context, providerID string) error {
	if !providerIDPattern.MatchString(providerID) {
		return fmt.Errorf("invalid redline provider id %q", providerID)
	}

	ctx, cancel := withTimeout(ctx, RefreshTimeout)
	defer cancel()

	req, err := c.newRequest(ctx, http.MethodPost, "/v1/providers/"+url.PathEscape(providerID)+"/refresh")
	if err != nil {
		return err
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	// Drain a bounded prefix so the connection can be reused; a longer body is
	// not worth reading just to discard it.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))

	return statusError(resp)
}

// DiscoverToken resolves the Redline API token: the REDLINE_API_TOKEN
// environment variable wins, then an explicit tokenFile, then the platform
// default beside redline.yaml. It also returns the path the token came from
// (empty for the environment variable) so callers can name it in a
// "token not found at …" message.
func DiscoverToken(tokenFile string) (token string, path string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return discoverToken(tokenFile, os.Getenv, home, runtime.GOOS)
}

// discoverToken is DiscoverToken with its environment injected, so the lookup
// stays testable without those seams in the exported signature.
func discoverToken(tokenFile string, getenv func(string) string, homeDir string, goos string) (string, string, error) {
	if token := strings.TrimSpace(getenv("REDLINE_API_TOKEN")); token != "" {
		return token, "", nil
	}

	path := tokenFile
	if path == "" {
		path = defaultTokenPath(homeDir, goos)
	}

	if err := checkTokenFile(path); err != nil {
		return "", path, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", path, fmt.Errorf("redline API token not found at %s: %w", path, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", path, fmt.Errorf("redline API token not found at %s: file is empty", path)
	}
	return token, path, nil
}

// checkTokenFile refuses a token path that is not a private regular file:
// Lstat rather than Stat so a symlink is caught instead of followed, and a
// group- or world-readable mode means the token has already leaked to any other
// account on the machine.
func checkTokenFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("redline API token not found at %s: %w", path, err)
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("redline token file %s is a symlink; point the config at the real file", path)
	}
	if !mode.IsRegular() {
		return fmt.Errorf("redline token file %s is not a regular file", path)
	}
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("redline token file %s is readable by other users (mode %04o); chmod 600 it", path, mode.Perm())
	}
	return nil
}

// defaultTokenPath is where Redline stores its api-token per platform.
func defaultTokenPath(homeDir, goos string) string {
	if goos == "darwin" {
		return filepath.Join(homeDir, "Library", "Application Support", "Redline", "api-token")
	}
	return filepath.Join(homeDir, ".config", "redline", "api-token")
}

// statusError maps a non-2xx Redline response onto a typed error.
func statusError(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		// The client never follows these, so the token stayed on this host; say so
		// rather than reporting an opaque unexpected status.
		return fmt.Errorf("redline returned a redirect (%d)", resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w (status %d)", ErrUnauthorized, resp.StatusCode)
	default:
		return fmt.Errorf("redline %s: unexpected status %d", resp.Request.URL.Path, resp.StatusCode)
	}
}
