package usage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func mustMap(t *testing.T, raw []byte, now time.Time) Usage {
	t.Helper()
	u, err := MapDashboard(raw, now)
	if err != nil {
		t.Fatalf("MapDashboard: %v", err)
	}
	return u
}

func findProvider(t *testing.T, u Usage, id string) Provider {
	t.Helper()
	for _, p := range u.Providers {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("provider %q not found in %+v", id, u.Providers)
	return Provider{}
}

func TestMapDashboardReportsProvidersSortedByID(t *testing.T) {
	now := time.Date(2026, 9, 4, 17, 2, 11, 0, time.UTC)

	u := mustMap(t, loadFixture(t, "dashboard.json"), now)

	if u.State != StateOK {
		t.Errorf("State = %q, want %q", u.State, StateOK)
	}
	if !u.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", u.UpdatedAt, now)
	}
	if len(u.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(u.Providers))
	}
	if got, want := u.Providers[0].ID, "claude-main"; got != want {
		t.Errorf("Providers[0].ID = %q, want %q", got, want)
	}
	if got, want := u.Providers[0].Provider, "claude"; got != want {
		t.Errorf("Providers[0].Provider = %q, want %q", got, want)
	}
	if got, want := u.Providers[0].Label, "Claude"; got != want {
		t.Errorf("Providers[0].Label = %q, want %q", got, want)
	}
	if got, want := u.Providers[1].ID, "codex-main"; got != want {
		t.Errorf("Providers[1].ID = %q, want %q", got, want)
	}
	if got, want := u.Providers[1].Label, "Codex"; got != want {
		t.Errorf("Providers[1].Label = %q, want %q", got, want)
	}
}

func TestMapDashboardOrdersWindowsAccountFirstThenModelScoped(t *testing.T) {
	now := time.Date(2026, 9, 4, 17, 2, 11, 0, time.UTC)
	u := mustMap(t, loadFixture(t, "dashboard.json"), now)

	t.Run("claude", func(t *testing.T) {
		claude := findProvider(t, u, "claude-main")
		want := []Window{
			{Key: "session", Label: "5-hour window", Scope: "account", Remaining: 0.56,
				ResetsAt: time.Date(2026, 9, 4, 20, 50, 0, 0, time.UTC), PeriodSeconds: 18000},
			{Key: "weekly", Label: "Weekly", Scope: "account", Remaining: 0.15,
				ResetsAt: time.Date(2026, 9, 4, 16, 59, 59, 0, time.UTC), PeriodSeconds: 604800},
			{Key: "model:fable:weekly", Label: "Fable", Scope: "model", Remaining: 0.45,
				ResetsAt: time.Date(2026, 9, 4, 16, 59, 59, 0, time.UTC), PeriodSeconds: 604800},
		}
		assertWindows(t, claude.Windows, want)
	})

	t.Run("codex model scoped short before weekly", func(t *testing.T) {
		codex := findProvider(t, u, "codex-main")
		want := []Window{
			{Key: "weekly", Label: "Weekly", Scope: "account", Remaining: 0,
				ResetsAt: time.Date(2026, 9, 7, 3, 3, 2, 0, time.UTC), PeriodSeconds: 604800},
			{Key: "model:spark:short", Label: "Spark", Scope: "model", Remaining: 0.55,
				ResetsAt: time.Date(2026, 9, 4, 19, 36, 0, 0, time.UTC), PeriodSeconds: 18000, ResetInferred: true},
			{Key: "model:spark:weekly", Label: "Spark Weekly", Scope: "model", Remaining: 0.6,
				ResetsAt: time.Date(2026, 9, 7, 3, 3, 2, 0, time.UTC), PeriodSeconds: 604800},
		}
		assertWindows(t, codex.Windows, want)
	})
}

func TestMapDashboardLabelsWindowsWithoutASourceLabel(t *testing.T) {
	raw := []byte(`{"providers":[{"id":"a","provider":"codex","snapshot":{"allowances":[
		{"key":"session","scope":"account","role":"short","period_duration_seconds":18000},
		{"key":"weekly","scope":"account","role":"weekly","period_duration_seconds":604800},
		{"key":"model:spark:weekly","scope":"model","role":"weekly","period_duration_seconds":604800}]}}]}`)

	p := findProvider(t, mustMap(t, raw, time.Now()), "a")

	got := make([]string, 0, len(p.Windows))
	for _, w := range p.Windows {
		got = append(got, w.Label)
	}
	// The account windows get Redline's expanded-view names; a model pool with no
	// source_label shows its raw key rather than a guessed prettier spelling.
	want := []string{"5-hour window", "Weekly", "model:spark:weekly"}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("label[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMapDashboardPrimaryIsSmallestAccountWindow(t *testing.T) {
	now := time.Date(2026, 9, 4, 17, 2, 11, 0, time.UTC)
	u := mustMap(t, loadFixture(t, "dashboard.json"), now)

	claude := findProvider(t, u, "claude-main")
	if claude.Primary == nil {
		t.Fatal("claude Primary = nil, want the account session window")
	}
	wantClaude := Window{Key: "session", Label: "5h", Scope: "account", Remaining: 0.56,
		ResetsAt: time.Date(2026, 9, 4, 20, 50, 0, 0, time.UTC), PeriodSeconds: 18000}
	if *claude.Primary != wantClaude {
		t.Errorf("claude Primary = %+v, want %+v", *claude.Primary, wantClaude)
	}

	// The live Codex account has no account-scoped 5h window: weekly is primary.
	codex := findProvider(t, u, "codex-main")
	if codex.Primary == nil {
		t.Fatal("codex Primary = nil, want the account weekly window")
	}
	wantCodex := Window{Key: "weekly", Label: "wk", Scope: "account", Remaining: 0,
		ResetsAt: time.Date(2026, 9, 7, 3, 3, 2, 0, time.UTC), PeriodSeconds: 604800}
	if *codex.Primary != wantCodex {
		t.Errorf("codex Primary = %+v, want %+v", *codex.Primary, wantCodex)
	}
}

func TestMapDashboardProviderState(t *testing.T) {
	tests := []struct {
		name        string
		item        string
		wantState   ProviderState
		wantError   string
		wantWindows int
	}{
		{
			name:        "fresh snapshot is ok",
			item:        `{"id":"a","provider":"claude","snapshot":{"allowances":[{"key":"weekly","scope":"account","role":"weekly"}]},"snapshot_stale":false}`,
			wantState:   ProviderOK,
			wantWindows: 1,
		},
		{
			// Redline reports a per-account error alongside a usable fresh
			// snapshot, but that text can carry filesystem paths, so it must
			// never reach the browser while a snapshot is usable.
			name:        "error alongside a fresh snapshot is suppressed",
			item:        `{"id":"a","provider":"claude","error":"refresh failed: /Users/jfox/secret","snapshot":{"allowances":[{"key":"weekly","scope":"account","role":"weekly"}]},"snapshot_stale":false}`,
			wantState:   ProviderOK,
			wantError:   "",
			wantWindows: 1,
		},
		{
			name:        "stale snapshot keeps its windows",
			item:        `{"id":"a","provider":"claude","snapshot":{"allowances":[{"key":"weekly","scope":"account","role":"weekly"}]},"snapshot_stale":true}`,
			wantState:   ProviderStale,
			wantWindows: 1,
		},
		{
			name:      "missing snapshot is an error",
			item:      `{"id":"a","provider":"claude"}`,
			wantState: ProviderError,
			wantError: "no usage snapshot",
		},
		{
			name:      "missing snapshot reports the item error",
			item:      `{"id":"a","provider":"claude","error":"login expired","usage_source":{"last_error":"probe failed"}}`,
			wantState: ProviderError,
			wantError: "login expired",
		},
		{
			name:      "missing snapshot falls back to usage_source last_error",
			item:      `{"id":"a","provider":"claude","usage_source":{"last_error":"probe failed"}}`,
			wantState: ProviderError,
			wantError: "probe failed",
		},
		{
			// Same suppression applies to a stale-but-present snapshot.
			name:        "error alongside a stale snapshot is suppressed",
			item:        `{"id":"a","provider":"claude","error":"refresh failed: /Users/jfox/secret","snapshot":{"allowances":[{"key":"weekly","scope":"account","role":"weekly"}]},"snapshot_stale":true}`,
			wantState:   ProviderStale,
			wantError:   "",
			wantWindows: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"providers":[` + tc.item + `]}`)
			u := mustMap(t, raw, time.Now())
			p := findProvider(t, u, "a")
			if p.State != tc.wantState {
				t.Errorf("State = %q, want %q", p.State, tc.wantState)
			}
			if p.Error != tc.wantError {
				t.Errorf("Error = %q, want %q", p.Error, tc.wantError)
			}
			if len(p.Windows) != tc.wantWindows {
				t.Errorf("len(Windows) = %d, want %d", len(p.Windows), tc.wantWindows)
			}
		})
	}
}

func TestMapDashboardCapsProviderErrorAt200Runes(t *testing.T) {
	longError := strings.Repeat("é", 250) // multi-byte rune so a byte-based cap would corrupt it
	raw := []byte(`{"providers":[{"id":"a","provider":"claude","error":"` + longError + `"}]}`)

	p := findProvider(t, mustMap(t, raw, time.Now()), "a")

	if got := []rune(p.Error); len(got) != 200 {
		t.Fatalf("len(Error) = %d runes, want 200", len(got))
	}
	if !strings.HasPrefix(p.Error, strings.Repeat("é", 10)) {
		t.Errorf("Error = %q, want it to start with the original text", p.Error)
	}
}

func TestMapDashboardSynthesizesWindowsWhenAllowancesMissing(t *testing.T) {
	raw := []byte(`{"providers":[{"id":"a","provider":"claude","snapshot":{
		"short":{"remaining":0.4,"resets_at":"2026-09-04T20:50:00Z"},
		"weekly":{"remaining":0.2,"resets_at":"2026-09-07T03:03:02Z"}}}]}`)

	p := findProvider(t, mustMap(t, raw, time.Now()), "a")

	assertWindows(t, p.Windows, []Window{
		{Key: "session", Label: "5-hour window", Scope: "account", Remaining: 0.4,
			ResetsAt: time.Date(2026, 9, 4, 20, 50, 0, 0, time.UTC), PeriodSeconds: 18000},
		{Key: "weekly", Label: "Weekly", Scope: "account", Remaining: 0.2,
			ResetsAt: time.Date(2026, 9, 7, 3, 3, 2, 0, time.UTC), PeriodSeconds: 604800},
	})
	if p.Primary == nil || p.Primary.Key != "session" || p.Primary.Label != "5h" {
		t.Errorf("Primary = %+v, want the session window labelled 5h", p.Primary)
	}
}

func TestMapDashboardSynthesizesWeeklyOnlyWhenShortMissing(t *testing.T) {
	raw := []byte(`{"providers":[{"id":"a","provider":"codex","snapshot":{
		"weekly":{"remaining":0,"resets_at":"2026-09-07T03:03:02Z"}}}]}`)

	p := findProvider(t, mustMap(t, raw, time.Now()), "a")

	assertWindows(t, p.Windows, []Window{
		{Key: "weekly", Label: "Weekly", Scope: "account", Remaining: 0,
			ResetsAt: time.Date(2026, 9, 7, 3, 3, 2, 0, time.UTC), PeriodSeconds: 604800},
	})
	if p.Primary == nil || p.Primary.Label != "wk" {
		t.Errorf("Primary = %+v, want the weekly window labelled wk", p.Primary)
	}
}

func TestMapDashboardMarshalsEmptyArraysNotNull(t *testing.T) {
	raw := []byte(`{"providers":[{"id":"a","provider":"claude"}]}`)

	payload, err := json.Marshal(mustMap(t, raw, time.Now()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(payload), "null") {
		t.Errorf("payload = %s, want no null arrays", payload)
	}
}

func TestMapDashboardRejectsInvalidJSON(t *testing.T) {
	if _, err := MapDashboard([]byte("not json"), time.Now()); err == nil {
		t.Fatal("MapDashboard(invalid) = nil error, want an error")
	}
}

func assertWindows(t *testing.T, got, want []Window) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d windows %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("window[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestUsageStateConstantsEncodeAsPlainStrings(t *testing.T) {
	payload, err := json.Marshal(Usage{
		State:     StateOK,
		Providers: []Provider{{ID: "a", State: ProviderStale}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"state":"ok"`) {
		t.Errorf("payload = %s, want the usage state encoded as \"ok\"", payload)
	}
	if !strings.Contains(string(payload), `"state":"stale"`) {
		t.Errorf("payload = %s, want the provider state encoded as \"stale\"", payload)
	}
}

func TestDisabledReportsTheDisabledState(t *testing.T) {
	u := Disabled()

	if u.State != StateDisabled {
		t.Errorf("State = %q, want %q", u.State, StateDisabled)
	}
	if u.Message != "provider usage is disabled" {
		t.Errorf("Message = %q, want the disabled message", u.Message)
	}
	payload, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(payload), "null") {
		t.Errorf("payload = %s, want no null arrays", payload)
	}
}
