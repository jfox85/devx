package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/jfox85/devx/session"
)

// gatepostLogsPathPrefix is the devx-side mount point for the per-session
// Gatepost Logs UI reverse proxy. Requests under
// /api/gatepost/logs/proxy/{session}/... are forwarded to the session's local
// gatepost-logs server (bound to 127.0.0.1:PORT on the host), with the access
// token injected server-side.
//
// Proxying (rather than redirecting to the raw host:port) means the Logs UI is
// reachable wherever the devx web UI itself is reachable — including through
// the Caddy reverse proxy and the Cloudflare tunnel — without publishing the
// per-session logs port or leaking the token into the browser URL.
const gatepostLogsPathPrefix = "/api/gatepost/logs/proxy/"

// gatepostLogsProxyURL builds the devx-relative URL a browser should open to
// reach the proxied Logs UI for a session. The session name is percent-encoded
// (slashes included) so branch-style names occupy a single path segment.
func gatepostLogsProxyURL(sessionName string) string {
	return gatepostLogsPathPrefix + url.PathEscape(sessionName) + "/"
}

// handleGatepostLogsProxy reverse-proxies the per-session Gatepost Logs UI.
//
// Route: registered as a catch-all on gatepostLogsPathPrefix so the request
// path is parsed manually (mirroring the terminal proxy), which keeps
// branch-style session names that contain slashes working.
func handleGatepostLogsProxy(w http.ResponseWriter, r *http.Request) {
	// Parse the first path segment after the prefix, preserving %2F so a
	// branch-style session name stays a single segment. The frontend encodes the
	// name with encodeURIComponent / url.PathEscape, turning slashes into %2F.
	rawPath := r.URL.RawPath
	if rawPath == "" {
		rawPath = r.URL.Path
	}
	afterPrefix := strings.TrimPrefix(rawPath, gatepostLogsPathPrefix)
	encodedName, restEncoded, _ := strings.Cut(afterPrefix, "/")
	name, err := url.PathUnescape(encodedName)
	if err != nil || name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session path segment required"})
		return
	}

	store, err := session.LoadSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sess, ok := store.GetSession(name)
	if !ok || !sess.Target.Gatepost.Enabled || sess.Target.Gatepost.LogsURL == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "gatepost logs not found"})
		return
	}
	tokenBytes, err := os.ReadFile(sess.Target.Gatepost.LogsTokenPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "gatepost logs token not found"})
		return
	}
	token := strings.TrimSpace(string(tokenBytes))

	target, err := url.Parse(sess.Target.Gatepost.LogsURL)
	if err != nil || target.Host == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid gatepost logs url"})
		return
	}

	// The devx-side prefix this session is mounted under, e.g.
	// /api/gatepost/logs/proxy/jf-better-ui/ — rebuilt from the escaped name to
	// match what the browser actually sees.
	mountPrefix := gatepostLogsPathPrefix + url.PathEscape(name) + "/"

	// Backend path = everything after the session segment. restEncoded preserves
	// percent-encoding; the Logs UI uses only ASCII paths, so unescaping is safe.
	backendPath, err := url.PathUnescape(restEncoded)
	if err != nil {
		backendPath = restEncoded
	}
	backendPath = "/" + backendPath

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = backendPath
			req.URL.RawPath = ""
			req.Host = target.Host
			// Inject the access token server-side; the browser never sees it.
			req.Header.Set("Authorization", "Bearer "+token)
			// The Bearer token is authoritative; drop any client cookies destined
			// for the logs origin.
			req.Header.Del("Cookie")
		},
		ModifyResponse: func(resp *http.Response) error {
			return rewriteGatepostLogsResponse(resp, mountPrefix)
		},
	}
	proxy.ServeHTTP(w, r)
}

// rewriteGatepostLogsResponse rewrites absolute "/api/..." references in the
// Logs UI's HTML so they resolve under the devx mount prefix instead of the
// devx web root. The Logs UI is a single self-contained HTML document whose
// only absolute references are "/api/..." fetch/navigation paths, so a targeted
// string replacement is sufficient and avoids depending on the Logs UI gaining
// base-path awareness.
func rewriteGatepostLogsResponse(resp *http.Response, mountPrefix string) error {
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	rewritten := rewriteGatepostLogsHTML(body, mountPrefix)
	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return nil
}

// rewriteGatepostLogsHTML rewrites the absolute "/api/" references in the Logs
// UI HTML so they resolve under mountPrefix. It only rewrites occurrences that
// begin with a quote or backtick delimiter (i.e. the start of a string or
// template literal), so it cannot corrupt the already-prefixed path or unrelated
// substrings. mountPrefix is expected to end with a trailing slash.
func rewriteGatepostLogsHTML(body []byte, mountPrefix string) []byte {
	base := strings.TrimSuffix(mountPrefix, "/")
	rewritten := body
	for _, delim := range []string{`"`, "`"} {
		from := []byte(delim + "/api/")
		to := []byte(delim + base + "/api/")
		rewritten = bytes.ReplaceAll(rewritten, from, to)
	}
	return rewritten
}
