package usage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRedline is an httptest server whose dashboard body can change between polls.
type fakeRedline struct {
	*httptest.Server
	mu              sync.Mutex
	dashboardBody   string
	dashboardCode   int
	refreshedPaths  []string
	dashboardHits   int
	refreshCode     int
	refreshDelay    time.Duration
	refreshCodeByID map[string]int
}

func newFakeRedline(t *testing.T, body string) *fakeRedline {
	t.Helper()
	f := &fakeRedline{dashboardBody: body, dashboardCode: http.StatusOK, refreshCode: http.StatusAccepted}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if r.URL.Path == "/v1/dashboard" {
			f.dashboardHits++
			code, body := f.dashboardCode, f.dashboardBody
			f.mu.Unlock()
			w.WriteHeader(code)
			w.Write([]byte(body))
			return
		}
		f.refreshedPaths = append(f.refreshedPaths, r.URL.Path)
		code, delay := f.refreshCode, f.refreshDelay
		if id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/providers/"), "/refresh"); f.refreshCodeByID != nil {
			if override, ok := f.refreshCodeByID[id]; ok {
				code = override
			}
		}
		f.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeRedline) setDashboard(body string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dashboardBody = body
	f.dashboardCode = code
}

func (f *fakeRedline) setRefresh(code int, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshCode = code
	f.refreshDelay = delay
}

func (f *fakeRedline) failRefreshFor(providerID string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshCodeByID == nil {
		f.refreshCodeByID = map[string]int{}
	}
	f.refreshCodeByID[providerID] = code
}

func (f *fakeRedline) refreshes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.refreshedPaths...)
}

func (f *fakeRedline) dashboardCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dashboardHits
}

const twoProviderDashboard = `{"providers":[
	{"id":"claude-main","provider":"claude","snapshot":{"source":"openusage","allowances":[
		{"key":"session","scope":"account","role":"short","remaining":0.56,"period_duration_seconds":18000}]}},
	{"id":"codex-main","provider":"codex","snapshot":{"source":"native","allowances":[
		{"key":"weekly","scope":"account","role":"weekly","remaining":0,"period_duration_seconds":604800}]}}]}`

func TestPollerUnavailableMessages(t *testing.T) {
	t.Run("redline not running", func(t *testing.T) {
		f := newFakeRedline(t, twoProviderDashboard)
		url := f.URL
		f.Server.Close()

		u := (&Poller{Client: &Client{BaseURL: url}}).Poll(context.Background())

		if u.State != "unavailable" {
			t.Errorf("State = %q, want unavailable", u.State)
		}
		if u.Message != "Redline is not running on this host" {
			t.Errorf("Message = %q, want the not-running message", u.Message)
		}
	})

	t.Run("token rejected", func(t *testing.T) {
		f := newFakeRedline(t, twoProviderDashboard)
		f.setDashboard("", http.StatusUnauthorized)

		u := (&Poller{Client: &Client{BaseURL: f.URL}}).Poll(context.Background())

		if u.Message != "Redline rejected the API token" {
			t.Errorf("Message = %q, want the rejected-token message", u.Message)
		}
	})

	t.Run("slow redline reports a timeout, not a missing service", func(t *testing.T) {
		block := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-block
		}))
		defer func() { close(block); srv.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		u := (&Poller{Client: &Client{BaseURL: srv.URL}}).Poll(ctx)

		if u.State != StateUnavailable {
			t.Errorf("State = %q, want unavailable", u.State)
		}
		if u.Message != "Redline did not respond in time" {
			t.Errorf("Message = %q, want the timeout message", u.Message)
		}
	})

	t.Run("an unclassified failure reports a generic message and logs the detail", func(t *testing.T) {
		f := newFakeRedline(t, "not json")
		var logs bytes.Buffer
		p := &Poller{Client: &Client{BaseURL: f.URL}, Logger: log.New(&logs, "", 0)}

		u := p.Poll(context.Background())

		if u.State != StateUnavailable {
			t.Errorf("State = %q, want unavailable", u.State)
		}
		if u.Message != "Redline could not be read" {
			t.Errorf("Message = %q, want the generic message", u.Message)
		}
		if strings.Contains(u.Message, f.URL) {
			t.Errorf("Message = %q, want the Redline URL kept off the wire", u.Message)
		}
		if !strings.Contains(logs.String(), "parse redline dashboard") {
			t.Errorf("logs = %q, want the underlying failure logged", logs.String())
		}
	})
}

func TestPollerKeepsLastGoodProvidersWhileUnavailable(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	clock := time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)
	p := &Poller{
		Client:       &Client{BaseURL: f.URL},
		KeepStaleFor: 15 * time.Minute,
		Now:          func() time.Time { return clock },
	}

	if u := p.Poll(context.Background()); len(u.Providers) != 2 {
		t.Fatalf("first poll returned %d providers, want 2", len(u.Providers))
	}
	f.setDashboard("", http.StatusUnauthorized)

	clock = clock.Add(14 * time.Minute)
	u := p.Poll(context.Background())
	if u.State != "unavailable" {
		t.Fatalf("State = %q, want unavailable", u.State)
	}
	if len(u.Providers) != 2 {
		t.Errorf("len(Providers) = %d within KeepStaleFor, want the last good 2", len(u.Providers))
	}

	clock = clock.Add(2 * time.Minute)
	if u := p.Poll(context.Background()); len(u.Providers) != 0 {
		t.Errorf("len(Providers) = %d past KeepStaleFor, want 0", len(u.Providers))
	}
}

func TestPollerNotifiesOnlyWhenPayloadChanges(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	clock := time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)
	var changes []Usage
	p := &Poller{
		Client:   &Client{BaseURL: f.URL},
		Now:      func() time.Time { return clock },
		OnChange: func(u Usage) { changes = append(changes, u) },
	}

	p.Poll(context.Background())
	p.Poll(context.Background())
	if len(changes) != 1 {
		t.Fatalf("OnChange called %d times for an unchanged dashboard, want 1", len(changes))
	}

	f.setDashboard(`{"providers":[{"id":"claude-main","provider":"claude","snapshot":{"source":"openusage","allowances":[
		{"key":"session","scope":"account","role":"short","remaining":0.1,"period_duration_seconds":18000}]}}]}`, http.StatusOK)
	p.Poll(context.Background())
	if len(changes) != 2 {
		t.Fatalf("OnChange called %d times after a change, want 2", len(changes))
	}
	if len(changes[1].Providers) != 1 {
		t.Errorf("second OnChange payload had %d providers, want 1", len(changes[1].Providers))
	}
}

func TestPollerDoesNotNotifyWhenOnlyTheClockAdvanced(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	clock := time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)
	var changes []Usage
	p := &Poller{
		Client:   &Client{BaseURL: f.URL},
		Now:      func() time.Time { clock = clock.Add(30 * time.Second); return clock },
		OnChange: func(u Usage) { changes = append(changes, u) },
	}

	p.Poll(context.Background())
	p.Poll(context.Background())

	if len(changes) != 1 {
		t.Fatalf("OnChange called %d times for an unchanged dashboard on a moving clock, want 1", len(changes))
	}
}

func TestPollerRefreshRefreshesEachProviderThenRereadsDashboard(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	clock := time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)
	p := &Poller{Client: &Client{BaseURL: f.URL}, Now: func() time.Time { return clock }}
	p.Poll(context.Background())
	pollsBefore := f.dashboardCount()

	clock = clock.Add(RefreshMinSpacing)
	res, err := p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.Throttled {
		t.Fatal("Refresh reported throttled, want it to run")
	}
	if res.Usage.State != StateOK {
		t.Errorf("State = %q, want ok", res.Usage.State)
	}

	got := f.refreshes()
	want := map[string]bool{
		"/v1/providers/claude-main/refresh": true,
		"/v1/providers/codex-main/refresh":  true,
	}
	if len(got) != len(want) {
		t.Fatalf("refresh paths = %v, want %d entries", got, len(want))
	}
	for _, path := range got {
		if !want[path] {
			t.Errorf("unexpected refresh path %q", path)
		}
	}
	if f.dashboardCount() != pollsBefore+1 {
		t.Errorf("dashboard reads = %d, want %d after the refresh re-read", f.dashboardCount(), pollsBefore+1)
	}
}

func TestPollerRefreshCapsConcurrentProviderRequests(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/providers/") {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Write([]byte(manyProviderDashboard(12)))
	}))
	defer srv.Close()

	p := &Poller{Client: &Client{BaseURL: srv.URL}}
	if u := p.Poll(context.Background()); len(u.Providers) != 12 {
		t.Fatalf("seed poll returned %d providers, want 12", len(u.Providers))
	}

	if _, err := p.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if peak > maxRefreshConcurrency {
		t.Errorf("peak concurrent refreshes = %d, want at most %d", peak, maxRefreshConcurrency)
	}
	if peak < 2 {
		t.Errorf("peak concurrent refreshes = %d, want the fan-out to still overlap", peak)
	}
}

// manyProviderDashboard builds a dashboard body with n distinct providers.
func manyProviderDashboard(n int) string {
	items := make([]string, 0, n)
	for i := 0; i < n; i++ {
		items = append(items, fmt.Sprintf(`{"id":"claude-%02d","provider":"claude","snapshot":{"source":"openusage","allowances":[`+
			`{"key":"session","scope":"account","role":"short","remaining":0.5,"period_duration_seconds":18000}]}}`, i))
	}
	return `{"providers":[` + strings.Join(items, ",") + `]}`
}

func TestPollerReturnsCopiesCallersCanMutate(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	p := &Poller{Client: &Client{BaseURL: f.URL}}

	polled := p.Poll(context.Background())
	polled.Providers[0].ID = "mutated"
	polled.Providers[0].Windows[0].Remaining = 0.99
	if polled.Providers[0].Primary != nil {
		polled.Providers[0].Primary.Remaining = 0.99
	}

	got := p.Current()
	if got.Providers[0].ID != "claude-main" {
		t.Errorf("Current().Providers[0].ID = %q after mutating the polled copy, want claude-main", got.Providers[0].ID)
	}
	if got.Providers[0].Windows[0].Remaining != 0.56 {
		t.Errorf("Current() window remaining = %v after mutating the polled copy, want 0.56", got.Providers[0].Windows[0].Remaining)
	}
	if got.Providers[0].Primary == nil || got.Providers[0].Primary.Remaining != 0.56 {
		t.Errorf("Current() primary = %+v after mutating the polled copy, want remaining 0.56", got.Providers[0].Primary)
	}

	got.Providers[0].Windows[0].Remaining = 0.01
	if again := p.Current(); again.Providers[0].Windows[0].Remaining != 0.56 {
		t.Errorf("Current() window remaining = %v after mutating an earlier Current(), want 0.56", again.Providers[0].Windows[0].Remaining)
	}
}

func TestPollerRefreshIsThrottled(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	clock := time.Date(2026, 9, 4, 17, 0, 0, 0, time.UTC)
	p := &Poller{Client: &Client{BaseURL: f.URL}, Now: func() time.Time { return clock }}
	p.Poll(context.Background())

	if res, err := p.Refresh(context.Background()); err != nil || res.Throttled {
		t.Fatalf("first Refresh = (%+v, %v), want it to run", res, err)
	}
	refreshesAfterFirst := len(f.refreshes())
	dashboardsAfterFirst := f.dashboardCount()

	clock = clock.Add(RefreshMinSpacing - time.Second)
	res, err := p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("throttled Refresh: %v", err)
	}
	if !res.Throttled {
		t.Error("second Refresh within the throttle window was not throttled")
	}
	if want := time.Second; res.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", res.RetryAfter, want)
	}
	if res.Usage.State != StateOK {
		t.Errorf("throttled Refresh returned state %q, want the cached ok state", res.Usage.State)
	}
	if len(f.refreshes()) != refreshesAfterFirst || f.dashboardCount() != dashboardsAfterFirst {
		t.Error("throttled Refresh contacted Redline, want no requests")
	}

	clock = clock.Add(2 * time.Second)
	if res, err := p.Refresh(context.Background()); err != nil || res.Throttled {
		t.Errorf("Refresh after the throttle window = (%+v, %v), want it to run", res, err)
	}
}

func TestPollerRefreshReportsProviderErrors(t *testing.T) {
	t.Run("every provider rejected reports the token error", func(t *testing.T) {
		f := newFakeRedline(t, twoProviderDashboard)
		p := &Poller{Client: &Client{BaseURL: f.URL}}
		p.Poll(context.Background())
		f.setRefresh(http.StatusUnauthorized, 0)

		res, err := p.Refresh(context.Background())

		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("error = %v, want errors.Is(ErrUnauthorized) so the handler can say the token was rejected", err)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("error = %q, want one clear line when every provider was rejected", err)
		}
		if res.Throttled {
			t.Error("Throttled = true, want false for a refresh that ran")
		}
		if res.Usage.State != StateOK {
			t.Errorf("Usage.State = %q, want the dashboard re-read to still succeed", res.Usage.State)
		}
	})

	t.Run("one failing provider joins into the error", func(t *testing.T) {
		f := newFakeRedline(t, twoProviderDashboard)
		p := &Poller{Client: &Client{BaseURL: f.URL}}
		p.Poll(context.Background())
		f.failRefreshFor("codex-main", http.StatusInternalServerError)

		res, err := p.Refresh(context.Background())

		if err == nil {
			t.Fatal("error = nil, want the failing provider reported")
		}
		if !strings.Contains(err.Error(), "codex-main") {
			t.Errorf("error = %v, want it to name codex-main", err)
		}
		if strings.Contains(err.Error(), "claude-main") {
			t.Errorf("error = %v, want the succeeding provider left out", err)
		}
		if res.Usage.State != StateOK {
			t.Errorf("Usage.State = %q, want the dashboard re-read to still succeed", res.Usage.State)
		}
	})
}

func TestPollerRefreshDoesNotLetACancelledCallerBlankTheCache(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	p := &Poller{Client: &Client{BaseURL: f.URL}}
	if u := p.Poll(context.Background()); u.State != StateOK {
		t.Fatalf("seed poll state = %q, want ok", u.State)
	}
	f.setRefresh(http.StatusAccepted, 300*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	p.Refresh(ctx)

	got := p.Current()
	if got.State != StateOK {
		t.Errorf("Current().State = %q after a cancelled caller, want the last good ok state", got.State)
	}
	if len(got.Providers) != 2 {
		t.Errorf("Current() has %d providers after a cancelled caller, want the last good 2", len(got.Providers))
	}
}

func TestPollerStartPollsUntilContextIsDone(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	polled := make(chan Usage, 8)
	p := &Poller{
		Client:   &Client{BaseURL: f.URL},
		Interval: time.Millisecond,
		OnChange: func(u Usage) { polled <- u },
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	select {
	case u := <-polled:
		if len(u.Providers) != 2 {
			t.Errorf("first polled payload had %d providers, want 2", len(u.Providers))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start never polled")
	}

	cancel()
	if got := p.Current(); got.State != "ok" {
		t.Errorf("Current() state = %q, want ok", got.State)
	}

	// Give the loop a moment to observe cancellation and stop hitting Redline.
	time.Sleep(20 * time.Millisecond)
	stopped := f.dashboardCount()
	time.Sleep(20 * time.Millisecond)
	if f.dashboardCount() != stopped {
		t.Error("Start kept polling after the context was cancelled")
	}
}

func TestPollerLogsStateTransitionsOnceNotEveryPoll(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	var logs bytes.Buffer
	p := &Poller{Client: &Client{BaseURL: f.URL}, Logger: log.New(&logs, "", 0)}

	p.Poll(context.Background())
	p.Poll(context.Background())
	if logs.Len() != 0 {
		t.Fatalf("logged %q while Redline stayed available, want silence", logs.String())
	}

	f.setDashboard("", http.StatusUnauthorized)
	p.Poll(context.Background())
	p.Poll(context.Background())
	if got := strings.Count(logs.String(), "unavailable"); got != 1 {
		t.Errorf("logged the unavailable transition %d times in %q, want 1", got, logs.String())
	}

	f.setDashboard(twoProviderDashboard, http.StatusOK)
	p.Poll(context.Background())
	p.Poll(context.Background())
	if got := strings.Count(logs.String(), "available again"); got != 1 {
		t.Errorf("logged the recovery %d times in %q, want 1", got, logs.String())
	}
}

func TestPollerWithoutALoggerStaysSilent(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	f.setDashboard("", http.StatusUnauthorized)
	var globalLog bytes.Buffer
	log.SetOutput(&globalLog)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	p := &Poller{Client: &Client{BaseURL: f.URL}}
	p.Poll(context.Background())
	p.Refresh(context.Background())

	if globalLog.Len() != 0 {
		t.Errorf("wrote %q to the global logger, want nothing so a TUI display is not corrupted", globalLog.String())
	}
}

func TestPollerRefreshLogsFailuresOnceAsOneLine(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	var logs bytes.Buffer
	p := &Poller{Client: &Client{BaseURL: f.URL}, Logger: log.New(&logs, "", 0)}
	p.Poll(context.Background())
	f.failRefreshFor("claude-main", http.StatusInternalServerError)
	f.failRefreshFor("codex-main", http.StatusServiceUnavailable)

	p.Refresh(context.Background())

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("refresh logged %d lines %q, want one joined line", len(lines), logs.String())
	}
	for _, id := range []string{"claude-main", "codex-main"} {
		if !strings.Contains(lines[0], id) {
			t.Errorf("log line %q does not mention %q", lines[0], id)
		}
	}
}

func TestPollerSurvivesConcurrentRefreshAndCurrent(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	p := &Poller{Client: &Client{BaseURL: f.URL}, Interval: time.Millisecond, OnChange: func(Usage) {}}

	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)

	deadline := time.Now().Add(200 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				p.Refresh(ctx)
				if u := p.Current(); u.Providers != nil {
					u.Providers = append(u.Providers, Provider{ID: "scratch"})
				}
			}
		}()
	}
	wg.Wait()

	cancel()
	<-done
}

func TestPollerStartClosesDoneWhenTheLoopExits(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	p := &Poller{Client: &Client{BaseURL: f.URL}, Interval: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)

	select {
	case <-done:
		t.Fatal("done closed before the context was cancelled")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("done never closed after cancel; Shutdown cannot wait for the poll loop")
	}
}

func TestPollerStartClampsIntervalBelowMinimumToMinPollInterval(t *testing.T) {
	if got := effectivePollInterval(1 * time.Millisecond); got != MinPollInterval {
		t.Errorf("effectivePollInterval(1ms) = %v, want MinPollInterval (%v)", got, MinPollInterval)
	}
	if got := effectivePollInterval(MinPollInterval); got != MinPollInterval {
		t.Errorf("effectivePollInterval(MinPollInterval) = %v, want it unchanged", got)
	}
	if got := effectivePollInterval(time.Hour); got != time.Hour {
		t.Errorf("effectivePollInterval(1h) = %v, want it unchanged", got)
	}
	if got := effectivePollInterval(0); got != DefaultPollInterval {
		t.Errorf("effectivePollInterval(0) = %v, want DefaultPollInterval (%v)", got, DefaultPollInterval)
	}
	if got := effectivePollInterval(-1); got != DefaultPollInterval {
		t.Errorf("effectivePollInterval(-1) = %v, want DefaultPollInterval (%v)", got, DefaultPollInterval)
	}
}

func TestPollerStartTwicePanics(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	p := &Poller{Client: &Client{BaseURL: f.URL}, Interval: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := p.Start(ctx)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("second Start did not panic; two loops would share one cache")
		}
		cancel()
		<-done
	}()
	p.Start(ctx)
}

func TestPollerCurrentBeforeFirstPollIsLoading(t *testing.T) {
	p := &Poller{}

	u := p.Current()

	if u.State != StateLoading {
		t.Errorf("State = %q, want %q", u.State, StateLoading)
	}
	if u.Message != "" {
		t.Errorf("Message = %q, want empty so clients render the loading state, not an outage", u.Message)
	}
}

func TestPollerPollStoresMappedUsage(t *testing.T) {
	f := newFakeRedline(t, twoProviderDashboard)
	p := &Poller{Client: &Client{BaseURL: f.URL}}

	u := p.Poll(context.Background())

	if u.State != "ok" {
		t.Fatalf("State = %q (message %q), want ok", u.State, u.Message)
	}
	if len(u.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(u.Providers))
	}
	if got := p.Current(); got.State != "ok" || len(got.Providers) != 2 {
		t.Errorf("Current() = %+v, want the polled usage", got)
	}
}
