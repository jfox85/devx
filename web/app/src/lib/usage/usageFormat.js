// web/app/src/lib/usage/usageFormat.js
//
// Pure, DOM-free formatting helpers for the provider usage widget
// (UsageStrip.svelte, UsageDetailModal.svelte). Kept dependency-free so they
// can be unit tested with plain node/vitest — no Svelte, no fetch.

const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

// toMillis parses a Date, epoch millis, or ISO/RFC3339 string into epoch
// millis. Returns null for anything invalid/absent so callers can render a
// calm placeholder instead of "NaN" or "Invalid Date".
function toMillis(value) {
  if (value == null) return null
  if (value instanceof Date) {
    const t = value.getTime()
    return Number.isFinite(t) ? t : null
  }
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : null
  }
  if (typeof value === 'string') {
    const t = Date.parse(value)
    return Number.isFinite(t) ? t : null
  }
  return null
}

function pad2(n) {
  return String(n).padStart(2, '0')
}

// tone classifies a 0..1 remaining fraction against Redline's web dashboard
// thresholds: <15% remaining is danger, <35% is warn, else ok.
export function tone(remaining) {
  if (typeof remaining !== 'number' || !Number.isFinite(remaining)) return 'ok'
  if (remaining < 0.15) return 'danger'
  if (remaining < 0.35) return 'warn'
  return 'ok'
}

// percent converts a 0..1 remaining fraction into an integer 0..100,
// clamping out-of-range/invalid input rather than propagating NaN.
export function percent(remaining) {
  if (typeof remaining !== 'number' || !Number.isFinite(remaining)) return 0
  return Math.min(100, Math.max(0, Math.round(remaining * 100)))
}

// relativeReset renders a compact "time until resetsAt" string: '47m', '2h',
// '3d', or 'now' once the reset has passed (or is effectively immediate).
// Returns '—' for a null/invalid resetsAt.
export function relativeReset(resetsAt, now = new Date()) {
  const target = toMillis(resetsAt)
  const base = toMillis(now)
  if (target == null || base == null) return '—'
  const ms = target - base
  if (ms <= 0) return 'now'
  const minutes = Math.round(ms / 60000)
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(ms / 3600000)
  if (hours < 24) return `${hours}h`
  const days = Math.round(ms / 86400000)
  return `${days}d`
}

// absoluteReset renders a wall-clock reset time: 'HH:MM' when resetsAt falls
// on the same calendar day as `now` (local time), otherwise 'Mon HH:MM'.
// Returns '—' for a null/invalid resetsAt.
export function absoluteReset(resetsAt, now = new Date()) {
  const target = toMillis(resetsAt)
  const base = toMillis(now)
  if (target == null || base == null) return '—'
  const targetDate = new Date(target)
  const baseDate = new Date(base)
  const hhmm = `${pad2(targetDate.getHours())}:${pad2(targetDate.getMinutes())}`
  const sameDay = targetDate.getFullYear() === baseDate.getFullYear()
    && targetDate.getMonth() === baseDate.getMonth()
    && targetDate.getDate() === baseDate.getDate()
  if (sameDay) return hhmm
  return `${WEEKDAYS[targetDate.getDay()]} ${hhmm}`
}

// sampleAge renders a compact "how long ago" string for observed_at/polled_at
// timestamps: '4m', '2h', '3d', or 'now' for anything under a minute old.
// Returns '—' for a null/invalid observedAt.
export function sampleAge(observedAt, now = new Date()) {
  const observed = toMillis(observedAt)
  const base = toMillis(now)
  if (observed == null || base == null) return '—'
  const ms = Math.max(0, base - observed)
  const minutes = Math.round(ms / 60000)
  if (minutes < 1) return 'now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.round(ms / 3600000)
  if (hours < 24) return `${hours}h`
  const days = Math.round(ms / 86400000)
  return `${days}d`
}

// toneTextClass/toneBarClass map a tone to the repo's Tailwind palette
// (see SessionList.svelte: cyan accent, amber warn, red danger, gray dim).
export function toneTextClass(t) {
  switch (t) {
    case 'danger': return 'text-red-400'
    case 'warn': return 'text-amber-300'
    default: return 'text-gray-300'
  }
}

export function toneBarClass(t) {
  switch (t) {
    case 'danger': return 'bg-red-500'
    case 'warn': return 'bg-amber-400'
    default: return 'bg-cyan-500'
  }
}

// sampleAgePhrase renders a "time since" phrase suitable for prose, turning
// sampleAge's compact values into readable text: "4m ago", "just now", and
// "at an unknown time" for a missing/invalid timestamp. Callers embed the
// result directly rather than appending their own " ago".
export function sampleAgePhrase(observedAt, now = new Date()) {
  const age = sampleAge(observedAt, now)
  if (age === '—') return 'at an unknown time'
  if (age === 'now') return 'just now'
  return `${age} ago`
}

// staleLabel renders the strip's "· stale 22m" suffix, or '' when fresh.
export function staleLabel(observedAt, now = new Date()) {
  const age = sampleAge(observedAt, now)
  if (age === '—') return ''
  return `stale ${age}`
}

// providerDisplayName picks a human label for a provider entry: prefer the
// server-provided `label` (already capitalized, e.g. "Claude"), otherwise
// title-case the raw `provider` id as a fallback.
function providerDisplayName(provider) {
  if (provider?.label) return provider.label
  const id = provider?.provider
  if (!id) return 'Provider'
  return id.charAt(0).toUpperCase() + id.slice(1)
}

// windowLabelWords expands the strip's compact primary-window label ("5h",
// "wk") into a screen-reader-friendly phrase. Unknown/short labels are used
// as-is; a missing label falls back to the bare word "window".
function windowLabelWords(label) {
  if (!label) return 'window'
  if (label.toLowerCase() === 'wk') return 'weekly window'
  return `${label} window`
}

// stripAriaLabel composes ONE summarizing accessible name for the strip's
// outer button from the current usage data, e.g.:
//   "Provider usage: Claude 56% remaining, 5h window; Codex 0% remaining, weekly window"
// Stale/error providers are called out in words rather than relying on color:
//   "Claude usage stale", "Codex usage unavailable"
// Pure and DOM-free so it can be unit tested directly.
export function stripAriaLabel(usage) {
  const providers = usage?.providers || []
  if (providers.length === 0) return 'Provider usage'
  const parts = providers.map(provider => {
    const name = providerDisplayName(provider)
    if (provider.state === 'error') return `${name} usage unavailable`
    if (provider.state === 'stale') return `${name} usage stale`
    const pct = percent(provider.primary?.remaining)
    return `${name} ${pct}% remaining, ${windowLabelWords(provider.primary?.label)}`
  })
  return `Provider usage: ${parts.join('; ')}`
}
