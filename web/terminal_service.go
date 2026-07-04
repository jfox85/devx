package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jfox85/devx/session"
)

const (
	terminalPrewarmLimit       = 3
	terminalPrewarmIdleTimeout = 3 * time.Minute
	terminalSendInputMaxBytes  = 64 << 10 // 64 KiB
	terminalWriteRateLimit     = 30
	terminalWriteRateWindow    = time.Minute
)

var terminalWrites = newSimpleRateLimiter(terminalWriteRateLimit, terminalWriteRateWindow)

type simpleRateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newSimpleRateLimiter(limit int, window time.Duration) *simpleRateLimiter {
	return &simpleRateLimiter{limit: limit, window: window, hits: make(map[string][]time.Time)}
}

func (l *simpleRateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	recent := l.hits[key][:0]
	for _, hit := range l.hits[key] {
		if hit.After(cutoff) {
			recent = append(recent, hit)
		}
	}
	if len(recent) >= l.limit {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

type terminalStartReason string

const (
	terminalStartOpen    terminalStartReason = "open"
	terminalStartPrewarm terminalStartReason = "prewarm"
)

type terminalState string

const (
	terminalStateNotStarted terminalState = "not_started"
	terminalStateStarting   terminalState = "starting"
	terminalStateReady      terminalState = "ready"
	terminalStateError      terminalState = "error"
	terminalStateCapped     terminalState = "capped"
)

type terminalStatus struct {
	Session string        `json:"session"`
	Ready   bool          `json:"ready"`
	Running bool          `json:"running"`
	State   terminalState `json:"state"`
	Error   string        `json:"error,omitempty"`
}

type terminalInput struct {
	Text   string `json:"text"`
	Submit bool   `json:"submit"`
	Mode   string `json:"mode"`
}

type terminalService struct {
	mu         sync.Mutex
	ttyd       *ttydManager
	loadStore  func() (*session.SessionStore, error)
	ensureTmux func(name, path string) error
	tmuxInput  func(bufferName, target, text string, submit bool) error
}

func newTerminalService(ttyd *ttydManager) *terminalService {
	return &terminalService{
		ttyd:       ttyd,
		loadStore:  session.LoadSessions,
		ensureTmux: session.EnsureTmuxSession,
		tmuxInput:  pasteTmuxBuffer,
	}
}

func (s *terminalService) Status(sessionName string) (terminalStatus, error) {
	if err := validateTerminalSessionName(sessionName); err != nil {
		return terminalStatus{}, err
	}
	if _, err := s.lookupSession(sessionName); err != nil {
		return terminalStatus{}, err
	}
	if st, ok := s.ttyd.statusForSession(sessionName); ok {
		st.Session = sessionName
		return st, nil
	}
	return terminalStatus{Session: sessionName, Ready: false, Running: false, State: terminalStateNotStarted}, nil
}

func (s *terminalService) EnsureReady(sessionName string, reason terminalStartReason) (terminalStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateTerminalSessionName(sessionName); err != nil {
		return terminalStatus{}, err
	}
	sess, err := s.lookupSession(sessionName)
	if err != nil {
		return terminalStatus{}, err
	}
	if st, ok := s.ttyd.statusForSession(sessionName); ok {
		st.Session = sessionName
		return st, nil
	}
	if reason == terminalStartPrewarm && s.ttyd.prewarmedCount() >= terminalPrewarmLimit {
		return terminalStatus{Session: sessionName, Ready: false, Running: false, State: terminalStateCapped}, nil
	}
	if reason != terminalStartPrewarm {
		if err := s.ensureTmux(sessionName, sess.Path); err != nil {
			return terminalStatus{}, terminalHTTPError{status: http.StatusInternalServerError, message: fmt.Sprintf("failed to restore tmux session %q", sessionName), err: err}
		}
	}
	_, err = s.ttyd.startForSession(sessionName)
	if err != nil {
		if reason == terminalStartPrewarm && strings.Contains(err.Error(), "does not exist") {
			return terminalStatus{Session: sessionName, Ready: false, Running: false, State: terminalStateNotStarted}, nil
		}
		return terminalStatus{}, terminalHTTPError{status: http.StatusInternalServerError, message: "failed to start terminal", err: err}
	}
	if reason == terminalStartPrewarm {
		s.ttyd.markPrewarmed(sessionName, terminalPrewarmIdleTimeout)
	}
	st, _ := s.ttyd.statusForSession(sessionName)
	st.Session = sessionName
	return st, nil
}

// ProxyTarget determines the session and ttyd port for a /terminal/* request.
// It preserves the existing URL parsing semantics: initial iframe requests use
// an encoded first segment; ttyd asset requests can use decoded slashes and are
// resolved by prefix-matching active sessions.
func (s *terminalService) ProxyTarget(r *http.Request) (sessionName string, port int, err error) {
	rawPath := r.URL.RawPath
	if rawPath == "" {
		rawPath = r.URL.Path
	}
	encodedPart, _, _ := strings.Cut(strings.TrimPrefix(rawPath, "/terminal/"), "/")
	decoded, _ := url.PathUnescape(encodedPart)

	if decoded != "" {
		if p, ok := s.ttyd.portForSession(decoded); ok {
			return decoded, p, nil
		}
	}

	decodedPath := strings.TrimPrefix(r.URL.Path, "/terminal/")
	if name, p, ok := s.ttyd.findSessionByPathPrefix(decodedPath); ok {
		return name, p, nil
	}

	if decoded == "" {
		return "", 0, nil
	}
	if _, err := s.EnsureReady(decoded, terminalStartOpen); err != nil {
		return "", 0, err
	}
	p, ok := s.ttyd.portForSession(decoded)
	if !ok {
		return "", 0, terminalHTTPError{status: http.StatusInternalServerError, message: "terminal did not become ready"}
	}
	return decoded, p, nil
}

func (s *terminalService) SendInput(sessionName string, input terminalInput) error {
	if err := validateTerminalSessionName(sessionName); err != nil {
		return err
	}
	if _, err := s.lookupSession(sessionName); err != nil {
		return err
	}
	if input.Mode == "" {
		input.Mode = "paste-buffer"
	}
	if input.Mode != "paste-buffer" && input.Mode != "literal" {
		return terminalHTTPError{status: http.StatusBadRequest, message: "unsupported send mode"}
	}
	if input.Text == "" {
		return terminalHTTPError{status: http.StatusBadRequest, message: "text is required"}
	}
	if len(input.Text) > terminalSendInputMaxBytes {
		return terminalHTTPError{status: http.StatusRequestEntityTooLarge, message: "text is too large"}
	}
	target := exactTmuxSessionTarget(sessionName) + ":"
	if input.Mode == "literal" {
		if err := execTmuxRun("send-keys", "-t", target, "-l", "--", input.Text); err != nil {
			return terminalHTTPError{status: http.StatusInternalServerError, message: "failed to send input", err: err}
		}
		if input.Submit {
			if err := execTmuxRun("send-keys", "-t", target, "Enter"); err != nil {
				return terminalHTTPError{status: http.StatusInternalServerError, message: "failed to submit input", err: err}
			}
		}
		return nil
	}
	bufferName, err := randomTmuxBufferName()
	if err != nil {
		return terminalHTTPError{status: http.StatusInternalServerError, message: "failed to allocate input buffer", err: err}
	}
	if err := s.tmuxInput(bufferName, target, input.Text, input.Submit); err != nil {
		return terminalHTTPError{status: http.StatusInternalServerError, message: "failed to send input", err: err}
	}
	return nil
}

func (s *terminalService) lookupSession(name string) (*session.Session, error) {
	store, err := s.loadStore()
	if err != nil {
		return nil, terminalHTTPError{status: http.StatusInternalServerError, message: "could not load session store", err: err}
	}
	if store == nil || store.Sessions == nil {
		return nil, terminalHTTPError{status: http.StatusNotFound, message: "session not found"}
	}
	sess, ok := store.Sessions[name]
	if !ok {
		return nil, terminalHTTPError{status: http.StatusNotFound, message: "session not found"}
	}
	return sess, nil
}

func validateTerminalSessionName(name string) error {
	if name == "" {
		return terminalHTTPError{status: http.StatusBadRequest, message: "session is required"}
	}
	if !session.IsValidSessionName(name) {
		return terminalHTTPError{status: http.StatusBadRequest, message: "invalid session name"}
	}
	return nil
}

type terminalHTTPError struct {
	status  int
	message string
	err     error
}

func (e terminalHTTPError) Error() string {
	if e.err == nil {
		return e.message
	}
	return e.message + ": " + e.err.Error()
}

func (e terminalHTTPError) Unwrap() error { return e.err }

func writeTerminalError(w http.ResponseWriter, err error) {
	var terminalErr terminalHTTPError
	if errors.As(err, &terminalErr) {
		writeJSON(w, terminalErr.status, map[string]string{"error": terminalErr.message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "terminal error"})
}

func terminalWriteGuard(w http.ResponseWriter, r *http.Request, maxBytes int64) bool {
	if !sameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden origin"})
		return false
	}
	if !terminalWrites.allow(rateLimitKey(r), time.Now()) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return false
	}
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	return true
}

func rateLimitKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	if host == "" {
		host = "unknown"
	}
	return host + " " + r.URL.Path
}

func sameOriginRequest(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser CLI/API clients may use an explicit bearer token without an
		// Origin header. Cookie-authenticated browser writes must still show
		// same-origin provenance via Fetch Metadata or Referer.
		return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || requestProvenanceMatchesHost(r)
	}
	// originMatchesHost honors X-Forwarded-Host so requests arriving through the
	// trusted reverse proxy (Caddy / Cloudflare tunnel) pass when the browser's
	// Origin is the external hostname rather than the rewritten upstream Host.
	return originMatchesHost(r)
}

func handleDecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}

func randomTmuxBufferName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "devx-" + hex.EncodeToString(b[:]), nil
}

func pasteTmuxBuffer(bufferName, target, text string, submit bool) error {
	load := exec.Command("tmux", "load-buffer", "-b", bufferName, "-")
	load.Stdin = strings.NewReader(text)
	if err := load.Run(); err != nil {
		return err
	}
	defer exec.Command("tmux", "delete-buffer", "-b", bufferName).Run() //nolint:errcheck
	if err := execTmuxRun("paste-buffer", "-b", bufferName, "-t", target); err != nil {
		return err
	}
	if submit {
		if err := execTmuxRun("send-keys", "-t", target, "Enter"); err != nil {
			return err
		}
	}
	return nil
}
