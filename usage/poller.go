package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// RefreshMinSpacing is the minimum time between two provider refreshes; extra
// requests reuse the cached usage.
const RefreshMinSpacing = 15 * time.Second

// maxRefreshConcurrency caps how many provider refreshes are in flight at once:
// each one is an upstream fetch on Redline's side, so a long account list must
// not turn one click into a burst of simultaneous requests.
const maxRefreshConcurrency = 4

// defaultKeepStaleFor is how long the last good providers are shown while
// Redline is unavailable, matching Redline's own snapshot staleness window.
const defaultKeepStaleFor = 15 * time.Minute

// Poller keeps a cached Usage snapshot fresh by polling Redline's dashboard.
// The zero value is usable except for Client, which must be set.
type Poller struct {
	Client   *Client
	Interval time.Duration
	// KeepStaleFor is how long the last good providers survive while Redline is
	// unavailable. Defaults to 15 minutes.
	KeepStaleFor time.Duration
	// Now defaults to time.Now; tests inject a clock.
	Now func() time.Time
	// OnChange is called whenever the marshalled payload differs from the
	// previous one.
	OnChange func(Usage)

	// Logger receives state transitions and refresh failures. A nil Logger
	// discards them, which is what a Bubble Tea TUI needs: stray writes to
	// stderr corrupt the display.
	Logger *log.Logger

	// pollMu single-flights Poll across fetch, store and notify. It is never
	// held while mu is held.
	pollMu sync.Mutex

	mu           sync.Mutex
	loaded       bool
	last         Usage
	lastPayload  []byte
	lastGood     []Provider
	lastGoodTime time.Time
	refreshing   bool
	lastRefresh  time.Time
	started      bool
	ownCtx       context.Context
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// logf writes one line to the injected logger, if any.
func (p *Poller) logf(format string, args ...any) {
	if p.Logger != nil {
		p.Logger.Printf(format, args...)
	}
}

func (p *Poller) keepStaleFor() time.Duration {
	if p.KeepStaleFor > 0 {
		return p.KeepStaleFor
	}
	return defaultKeepStaleFor
}

// DefaultPollInterval matches the plan's usage.poll_interval default;
// Redline's dashboard read is DB-only so this is cheap. cmd/root.go and
// web.UsageOptionsFromGlobalConfig build their viper/option defaults from this
// so there is one source of truth for the interval.
const DefaultPollInterval = 30 * time.Second

// MinPollInterval is the floor for usage.poll_interval: anything below this
// (including a misconfigured value that parses to a tiny duration, like a bare
// number of nanoseconds) is clamped up rather than hammering Redline.
const MinPollInterval = 5 * time.Second

// effectivePollInterval clamps interval to [MinPollInterval, +inf), and maps
// a non-positive interval (unset, or a config value that failed to parse) onto
// DefaultPollInterval. Both Start and the config-parsing layer
// (web.UsageOptionsFromGlobalConfig) apply this so a bad config value can
// never reach the poll loop as-is.
func effectivePollInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return DefaultPollInterval
	}
	if interval < MinPollInterval {
		return MinPollInterval
	}
	return interval
}

// Start polls Redline immediately and then every Interval until ctx is done.
// It returns as soon as the background loop is running; the returned channel is
// closed when the loop has exited, so Shutdown can wait for an in-flight poll.
// ctx also becomes the poller's own lifecycle context, which is what Refresh
// and the loop use for their Redline calls.
//
// Starting an already-started Poller panics: two loops over one cache is a
// programming error, not a runtime condition.
func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	// Defensive clamp: Start is the last line of defense even when a caller built
	// a Poller directly (bypassing the config-parsing floor).
	interval := effectivePollInterval(p.Interval)

	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		panic("usage: Poller.Start called twice on the same poller")
	}
	p.started = true
	p.ownCtx = ctx
	p.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Poll(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					// Shutdown raced the tick; do not record a cancelled poll.
					return
				}
				p.Poll(ctx)
			}
		}
	}()
	return done
}

// pollerCtx is the context the poller owns for its own Redline I/O: the one
// passed to Start, or the background context when Start was never called. A
// caller's request context must never drive a write to the shared cache.
func (p *Poller) pollerCtx() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ownCtx != nil {
		return p.ownCtx
	}
	return context.Background()
}

// Poll performs a single dashboard read and stores the result. It is single
// flighted: concurrent callers queue so OnChange can never deliver a payload
// older than Current(). Only the owner of the Poller should call it; other
// callers want Current, which never blocks on Redline.
func (p *Poller) Poll(ctx context.Context) Usage {
	p.pollMu.Lock()
	defer p.pollMu.Unlock()

	raw, err := p.Client.Dashboard(ctx)
	var u Usage
	if err == nil {
		u, err = MapDashboard(raw, p.now())
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		// Our own context was cancelled (shutdown), which says nothing about
		// Redline: leave the cache alone. A deadline that expired is different --
		// that is a real "Redline did not respond in time" observation.
		return p.Current()
	}
	now := p.now()

	p.mu.Lock()
	if err != nil {
		u = Usage{State: StateUnavailable, Message: unavailableMessage(err), UpdatedAt: now}
		if now.Sub(p.lastGoodTime) < p.keepStaleFor() {
			u.Providers = p.lastGood
		} else {
			p.lastGood = nil
		}
	} else {
		p.lastGood = u.Providers
		p.lastGoodTime = now
	}
	p.loaded = true
	previous := p.last
	p.last = u
	changed := p.recordPayload(u)
	p.mu.Unlock()

	if u.State != previous.State {
		if u.State == StateUnavailable {
			// The error, not u.Message: the diagnosis (URLs, upstream text) belongs
			// in the operator's log, never in the payload clients render.
			p.logf("usage: redline unavailable: %v", err)
		} else if previous.State == StateUnavailable {
			p.logf("usage: redline available again")
		}
	}

	if changed && p.OnChange != nil {
		p.OnChange(u.clone())
	}
	return u.clone()
}

// recordPayload stores the marshalled usage and reports whether it differs from
// the previous poll. UpdatedAt is excluded so an unchanged dashboard does not
// broadcast on every tick just because the clock moved. The caller must hold
// p.mu.
func (p *Poller) recordPayload(u Usage) bool {
	u.UpdatedAt = time.Time{}
	payload, err := json.Marshal(u)
	if err != nil {
		return false
	}
	if bytes.Equal(payload, p.lastPayload) {
		return false
	}
	p.lastPayload = payload
	return true
}

// unavailableMessage turns a poll failure into the short text DevX clients show.
// Unclassified failures collapse to one generic line: their text can carry the
// Redline URL or upstream detail, which has no place in a payload served to
// browsers. The detail is logged instead.
func unavailableMessage(err error) string {
	switch {
	case errors.Is(err, ErrUnreachable):
		return "Redline is not running on this host"
	case errors.Is(err, ErrUnauthorized):
		return "Redline rejected the API token"
	case errors.Is(err, ErrTimeout):
		return "Redline did not respond in time"
	default:
		return "Redline could not be read"
	}
}

// RefreshResult reports the outcome of Refresh.
type RefreshResult struct {
	// Usage is the state after the refresh, or the cached state when throttled.
	Usage Usage
	// Throttled is true when the call did not contact Redline because another
	// refresh is in flight or one ran less than RefreshMinSpacing ago.
	Throttled bool
	// RetryAfter is how long until a refresh would be accepted; only meaningful
	// when Throttled.
	RetryAfter time.Duration
}

// Refresh asks Redline to fetch live usage for every known provider and then
// re-reads the dashboard. It returns Throttled without contacting Redline when
// a refresh is already in flight or one ran less than RefreshMinSpacing ago.
//
// The returned error joins the per-provider refresh failures; the dashboard
// re-read still happens and Usage is still populated, so a caller can surface
// the error while showing fresh-as-possible data.
//
// ctx only aborts the caller's wait: the Redline calls and the cache write run
// on the poller's own context, so a disconnected browser can never blank the
// shared state for everybody else.
func (p *Poller) Refresh(ctx context.Context) (RefreshResult, error) {
	now := p.now()

	p.mu.Lock()
	if p.refreshing || now.Sub(p.lastRefresh) < RefreshMinSpacing {
		retryAfter := RefreshMinSpacing - now.Sub(p.lastRefresh)
		if retryAfter < 0 {
			retryAfter = 0
		}
		p.mu.Unlock()
		return RefreshResult{Usage: p.Current(), Throttled: true, RetryAfter: retryAfter}, nil
	}
	p.refreshing = true
	p.lastRefresh = now
	providers := p.last.Providers
	p.mu.Unlock()

	refreshCtx, cancel := context.WithTimeout(p.pollerCtx(), RefreshTimeout)

	type outcome struct {
		usage Usage
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		defer cancel()
		defer func() {
			p.mu.Lock()
			p.refreshing = false
			p.mu.Unlock()
		}()

		errs := make([]error, len(providers))
		slots := make(chan struct{}, maxRefreshConcurrency)
		var wg sync.WaitGroup
		for i, provider := range providers {
			wg.Add(1)
			go func(i int, id string) {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				if err := p.Client.Refresh(refreshCtx, id); err != nil {
					errs[i] = fmt.Errorf("refresh provider %q: %w", id, err)
				}
			}(i, provider.ID)
		}
		wg.Wait()

		err := joinRefreshErrors(errs)
		if err != nil {
			// errors.Join separates with newlines; keep it to one log line.
			p.logf("usage: refresh failed: %s", strings.ReplaceAll(err.Error(), "\n", "; "))
		}
		result <- outcome{usage: p.Poll(refreshCtx), err: err}
	}()

	select {
	case out := <-result:
		return RefreshResult{Usage: out.usage}, out.err
	case <-ctx.Done():
		// The caller stopped waiting; the refresh keeps running on the poller's
		// own context so the shared cache still gets the fresh read.
		return RefreshResult{Usage: p.Current()}, ctx.Err()
	}
}

// joinRefreshErrors joins the per-provider failures. When every provider was
// rejected for the same reason the joined text repeats it once per account, so
// the shared sentinel is returned on its own instead: the handler wants one
// clear "Redline rejected the API token", not two copies of it.
func joinRefreshErrors(errs []error) error {
	failures := 0
	unauthorized := 0
	for _, err := range errs {
		if err == nil {
			continue
		}
		failures++
		if errors.Is(err, ErrUnauthorized) {
			unauthorized++
		}
	}
	if failures > 0 && failures == unauthorized {
		return ErrUnauthorized
	}
	return errors.Join(errs...)
}

// Current returns the cached usage without contacting Redline.
func (p *Poller) Current() Usage {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.loaded {
		return Usage{State: StateLoading, Providers: []Provider{}}
	}
	return p.last.clone()
}
