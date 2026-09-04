// Package usage exposes provider subscription allowances (Claude, Codex) read
// from a Redline service running on the same host, mapped onto a DevX-owned
// wire shape so Redline's model never leaks into DevX clients.
package usage

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// State is the top-level usage state. It marshals as a plain JSON string.
type State string

const (
	// StateLoading is reported until the first poll completes.
	StateLoading State = "loading"
	// StateOK means the last Redline read succeeded.
	StateOK State = "ok"
	// StateUnavailable means Redline could not be read; Message says why.
	StateUnavailable State = "unavailable"
	// StateDisabled means provider usage is turned off by configuration.
	StateDisabled State = "disabled"
)

// ProviderState is one provider account's state. It marshals as a plain JSON
// string.
type ProviderState string

const (
	// ProviderOK means the snapshot is present and fresh.
	ProviderOK ProviderState = "ok"
	// ProviderStale means Redline flagged the snapshot as older than its
	// max_snapshot_age.
	ProviderStale ProviderState = "stale"
	// ProviderError means there is no usable snapshot for this account.
	ProviderError ProviderState = "error"
)

// Usage is the top-level payload DevX serves to its clients.
//
// Values returned by Poller.Current, Poller.Poll and Poller.OnChange are copies
// the caller owns: Providers, Windows and Primary may be read, sorted or edited
// in place without affecting the poller's cache.
type Usage struct {
	State State `json:"state"`
	// Message is human-readable text for the unavailable and disabled states.
	Message string `json:"message,omitempty"`
	// UpdatedAt is when DevX last read Redline successfully.
	UpdatedAt time.Time  `json:"updated_at"`
	Providers []Provider `json:"providers"`
}

// Disabled is the Usage served when provider usage is turned off, so the
// disabled wording lives here rather than in every consumer.
func Disabled() Usage {
	return Usage{State: StateDisabled, Message: "provider usage is disabled", Providers: []Provider{}}
}

// clone returns a copy that shares no mutable state with u.
func (u Usage) clone() Usage {
	if u.Providers == nil {
		return u
	}
	providers := make([]Provider, len(u.Providers))
	for i, p := range u.Providers {
		providers[i] = p.clone()
	}
	u.Providers = providers
	return u
}

// Provider is one Redline provider account (for example "claude-main").
type Provider struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`

	State ProviderState `json:"state"`
	// Error is populated only when State is ProviderError (no usable snapshot).
	// Redline's per-account error text can contain filesystem paths, so it is
	// deliberately dropped for the ok/stale cases where a usable snapshot exists
	// alongside it. Capped at 200 runes.
	Error      string    `json:"error,omitempty"`
	Source     string    `json:"source,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
	// Primary is the account-scoped window shown without a click; nil when the
	// account exposes no account-scoped window at all. When non-nil its Key
	// always matches exactly one Windows entry (the labels differ: "5h"/"wk"
	// here versus the expanded-view label in Windows).
	Primary *Window  `json:"primary,omitempty"`
	Windows []Window `json:"windows"`
}

// clone returns a copy that shares no mutable state with p.
func (p Provider) clone() Provider {
	if p.Primary != nil {
		primary := *p.Primary
		p.Primary = &primary
	}
	if p.Windows != nil {
		p.Windows = append([]Window(nil), p.Windows...)
	}
	return p
}

// Window is one allowance window (account- or model-scoped).
type Window struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Scope string `json:"scope"`
	// Remaining is the fraction of the allowance left, 0..1.
	Remaining     float64   `json:"remaining"`
	ResetsAt      time.Time `json:"resets_at"`
	PeriodSeconds int64     `json:"period_seconds,omitempty"`
	ResetInferred bool      `json:"reset_inferred,omitempty"`
}

// redlineDashboard is the subset of Redline's GET /v1/dashboard we consume.
type redlineDashboard struct {
	Providers []redlineProvider `json:"providers"`
}

type redlineProvider struct {
	ID            string           `json:"id"`
	Provider      string           `json:"provider"`
	Snapshot      *redlineSnapshot `json:"snapshot"`
	SnapshotStale bool             `json:"snapshot_stale"`
	Error         string           `json:"error"`
	UsageSource   struct {
		LastError string `json:"last_error"`
	} `json:"usage_source"`
}

type redlineSnapshot struct {
	ObservedAt time.Time          `json:"observed_at"`
	Source     string             `json:"source"`
	Short      *redlineLimit      `json:"short"`
	Weekly     *redlineLimit      `json:"weekly"`
	Allowances []redlineAllowance `json:"allowances"`
}

type redlineLimit struct {
	Remaining     float64   `json:"remaining"`
	ResetsAt      time.Time `json:"resets_at"`
	ResetInferred bool      `json:"reset_inferred"`
}

type redlineAllowance struct {
	Key           string    `json:"key"`
	SourceLabel   string    `json:"source_label"`
	Scope         string    `json:"scope"`
	Role          string    `json:"role"`
	Remaining     float64   `json:"remaining"`
	ResetsAt      time.Time `json:"resets_at"`
	PeriodSeconds int64     `json:"period_duration_seconds"`
	ResetInferred bool      `json:"reset_inferred"`
}

const (
	scopeAccount = "account"
	roleShort    = "short"
	roleWeekly   = "weekly"
)

// weeklyPeriodSeconds is the period Redline uses for weekly allowances.
const weeklyPeriodSeconds int64 = 604800

// MapDashboard maps a Redline GET /v1/dashboard payload onto the DevX wire
// shape. now becomes Usage.UpdatedAt.
func MapDashboard(raw []byte, now time.Time) (Usage, error) {
	var dash redlineDashboard
	if err := json.Unmarshal(raw, &dash); err != nil {
		return Usage{}, fmt.Errorf("parse redline dashboard: %w", err)
	}

	u := Usage{State: StateOK, UpdatedAt: now, Providers: make([]Provider, 0, len(dash.Providers))}
	for _, item := range dash.Providers {
		p := Provider{
			ID:       item.ID,
			Provider: item.Provider,
			Label:    providerLabel(item.Provider),
			State:    ProviderOK,
			Windows:  []Window{},
		}
		switch {
		case item.Snapshot == nil:
			p.State = ProviderError
			p.Error = capError(firstNonEmpty(item.Error, item.UsageSource.LastError, "no usage snapshot"))
		default:
			if item.SnapshotStale {
				p.State = ProviderStale
			}
			p.ObservedAt = item.Snapshot.ObservedAt
			p.Source = item.Snapshot.Source
			p.Windows = mapWindows(item.Snapshot)
			p.Primary = primaryWindow(p.Windows)
		}
		u.Providers = append(u.Providers, p)
	}
	sort.Slice(u.Providers, func(i, j int) bool { return u.Providers[i].ID < u.Providers[j].ID })
	return u, nil
}

// shortPeriodSeconds is the longest period still labelled as a 5-hour window.
const shortPeriodSeconds int64 = 18000

// primaryWindow picks the account-scoped window with the smallest period, which
// is the one shown without a click. Model-scoped pools are never primary.
// A window without a period is labelled "wk" rather than "5h" so an unlabelled
// weekly allowance is never advertised as a 5-hour window.
func primaryWindow(windows []Window) *Window {
	var best *Window
	for i := range windows {
		w := windows[i]
		if w.Scope != scopeAccount {
			continue
		}
		if best == nil || w.PeriodSeconds < best.PeriodSeconds {
			candidate := w
			best = &candidate
		}
	}
	if best == nil {
		return nil
	}
	if best.PeriodSeconds > 0 && best.PeriodSeconds <= shortPeriodSeconds {
		best.Label = "5h"
	} else {
		best.Label = "wk"
	}
	return best
}

// mapWindows converts a snapshot's allowances into ordered DevX windows:
// account short, account weekly, then model-scoped (short before weekly, then
// by label).
func mapWindows(snap *redlineSnapshot) []Window {
	allowances := append([]redlineAllowance(nil), snap.Allowances...)
	if len(allowances) == 0 {
		allowances = synthesizeAllowances(snap)
	}
	sort.SliceStable(allowances, func(i, j int) bool {
		ri := windowRank(allowances[i].Scope, allowances[i].Role)
		rj := windowRank(allowances[j].Scope, allowances[j].Role)
		if ri != rj {
			return ri < rj
		}
		return windowLabel(allowances[i]) < windowLabel(allowances[j])
	})

	windows := make([]Window, 0, len(allowances))
	for _, a := range allowances {
		windows = append(windows, Window{
			Key:           a.Key,
			Label:         windowLabel(a),
			Scope:         a.Scope,
			Remaining:     a.Remaining,
			ResetsAt:      a.ResetsAt,
			PeriodSeconds: a.PeriodSeconds,
			ResetInferred: a.ResetInferred,
		})
	}
	return windows
}

// synthesizeAllowances mirrors Redline's UsageSnapshot.AllAllowances() for
// snapshots that predate the allowances list: the account short and weekly
// limits become the only two windows.
func synthesizeAllowances(snap *redlineSnapshot) []redlineAllowance {
	var out []redlineAllowance
	if snap.Short != nil {
		out = append(out, redlineAllowance{
			Key:           "session",
			Scope:         scopeAccount,
			Role:          roleShort,
			Remaining:     snap.Short.Remaining,
			ResetsAt:      snap.Short.ResetsAt,
			PeriodSeconds: shortPeriodSeconds,
			ResetInferred: snap.Short.ResetInferred,
		})
	}
	if snap.Weekly != nil {
		out = append(out, redlineAllowance{
			Key:           "weekly",
			Scope:         scopeAccount,
			Role:          roleWeekly,
			Remaining:     snap.Weekly.Remaining,
			ResetsAt:      snap.Weekly.ResetsAt,
			PeriodSeconds: weeklyPeriodSeconds,
			ResetInferred: snap.Weekly.ResetInferred,
		})
	}
	return out
}

// windowRank orders account short (0), account weekly (1), model short (2),
// model weekly (3), anything else last.
func windowRank(scope, role string) int {
	if scope == scopeAccount {
		switch role {
		case roleShort:
			return 0
		case roleWeekly:
			return 1
		}
		return 2
	}
	switch role {
	case roleShort:
		return 3
	case roleWeekly:
		return 4
	}
	return 5
}

// windowLabel renames the account-scoped windows to match Redline's expanded
// view. Model pools use Redline's source_label, falling back to the raw key:
// rewriting the key would only invent a name Redline never chose.
func windowLabel(a redlineAllowance) string {
	if a.Scope == scopeAccount {
		switch a.Role {
		case roleShort:
			return "5-hour window"
		case roleWeekly:
			return "Weekly"
		}
	}
	if a.SourceLabel != "" {
		return a.SourceLabel
	}
	return a.Key
}

// maxProviderErrorRunes bounds Provider.Error so a pathological Redline error
// string cannot bloat the payload; it is diagnostic text, not data.
const maxProviderErrorRunes = 200

// capError truncates s to maxProviderErrorRunes runes (not bytes, so a
// multi-byte-heavy error is not corrupted mid-rune).
func capError(s string) string {
	runes := []rune(s)
	if len(runes) <= maxProviderErrorRunes {
		return s
	}
	return string(runes[:maxProviderErrorRunes])
}

// firstNonEmpty returns the first non-empty string, or "" when there is none.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// providerLabel title-cases a Redline provider kind ("claude" -> "Claude").
func providerLabel(kind string) string {
	if kind == "" {
		return ""
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}
