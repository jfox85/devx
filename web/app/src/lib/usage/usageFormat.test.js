import test from 'node:test'
import assert from 'node:assert/strict'
import {
  tone,
  percent,
  relativeReset,
  absoluteReset,
  sampleAge,
  sampleAgePhrase,
  toneTextClass,
  toneBarClass,
  stripAriaLabel,
} from './usageFormat.js'

test('tone classifies remaining fraction against Redline thresholds', () => {
  assert.equal(tone(0.14), 'danger')
  assert.equal(tone(0), 'danger')
  assert.equal(tone(0.15), 'warn')
  assert.equal(tone(0.34), 'warn')
  assert.equal(tone(0.35), 'ok')
  assert.equal(tone(1), 'ok')
  assert.equal(tone(NaN), 'ok')
  assert.equal(tone(undefined), 'ok')
})

test('percent rounds and clamps a 0..1 fraction to an integer 0..100', () => {
  assert.equal(percent(0.56), 56)
  assert.equal(percent(0.567), 57)
  assert.equal(percent(0), 0)
  assert.equal(percent(1), 100)
  assert.equal(percent(-0.2), 0)
  assert.equal(percent(1.2), 100)
  assert.equal(percent(NaN), 0)
  assert.equal(percent(undefined), 0)
})

test('relativeReset renders compact minute/hour/day windows', () => {
  const now = new Date('2026-09-04T16:00:00Z')
  assert.equal(relativeReset('2026-09-04T16:47:00Z', now), '47m')
  assert.equal(relativeReset('2026-09-04T18:00:00Z', now), '2h')
  assert.equal(relativeReset('2026-09-07T16:00:00Z', now), '3d')
})

test('relativeReset reports now for immediate/past resets', () => {
  const now = new Date('2026-09-04T16:00:00Z')
  assert.equal(relativeReset('2026-09-04T16:00:00Z', now), 'now')
  assert.equal(relativeReset('2026-09-04T15:00:00Z', now), 'now')
  assert.equal(relativeReset('2026-09-04T16:00:20Z', now), 'now')
})

test('relativeReset handles null/invalid input', () => {
  const now = new Date('2026-09-04T16:00:00Z')
  assert.equal(relativeReset(null, now), '—')
  assert.equal(relativeReset(undefined, now), '—')
  assert.equal(relativeReset('not-a-date', now), '—')
})

test('absoluteReset renders HH:MM for the same day, weekday HH:MM otherwise', () => {
  const now = new Date('2026-09-04T12:00:00')
  const sameDay = new Date('2026-09-04T20:50:00')
  const otherDay = new Date('2026-09-07T07:00:00') // Monday
  assert.equal(absoluteReset(sameDay, now), '20:50')
  assert.equal(absoluteReset(otherDay, now), 'Mon 07:00')
})

test('absoluteReset handles null/invalid input', () => {
  const now = new Date('2026-09-04T16:00:00Z')
  assert.equal(absoluteReset(null, now), '—')
  assert.equal(absoluteReset('garbage', now), '—')
})

test('sampleAge renders a compact "time since" string', () => {
  const now = new Date('2026-09-04T16:31:23Z')
  assert.equal(sampleAge('2026-09-04T16:27:23Z', now), '4m')
  assert.equal(sampleAge('2026-09-04T14:31:23Z', now), '2h')
  assert.equal(sampleAge('2026-09-01T16:31:23Z', now), '3d')
  assert.equal(sampleAge('2026-09-04T16:31:20Z', now), 'now')
})

test('sampleAge handles null/invalid input', () => {
  const now = new Date('2026-09-04T16:31:23Z')
  assert.equal(sampleAge(null, now), '—')
  assert.equal(sampleAge('nope', now), '—')
})

test('toneTextClass / toneBarClass map tones to the repo palette', () => {
  assert.equal(toneTextClass('danger'), 'text-red-400')
  assert.equal(toneTextClass('warn'), 'text-amber-300')
  assert.equal(toneTextClass('ok'), 'text-gray-300')
  assert.equal(toneBarClass('danger'), 'bg-red-500')
  assert.equal(toneBarClass('warn'), 'bg-amber-400')
  assert.equal(toneBarClass('ok'), 'bg-cyan-500')
})

test('sampleAgePhrase reads as prose without a caller-appended "ago"', () => {
  const now = new Date('2026-09-04T16:31:23Z')
  assert.equal(sampleAgePhrase('2026-09-04T16:27:23Z', now), '4m ago')
  // "now" and the invalid marker must not become "now ago" / "— ago".
  assert.equal(sampleAgePhrase('2026-09-04T16:31:20Z', now), 'just now')
  assert.equal(sampleAgePhrase(null, now), 'at an unknown time')
})

test('stripAriaLabel summarizes ok providers with percent and window words', () => {
  const usage = {
    providers: [
      { provider: 'claude', label: 'Claude', state: 'ok', primary: { label: '5h', remaining: 0.56 } },
      { provider: 'codex', label: 'Codex', state: 'ok', primary: { label: 'wk', remaining: 0 } },
    ],
  }
  assert.equal(
    stripAriaLabel(usage),
    'Provider usage: Claude 56% remaining, 5h window; Codex 0% remaining, weekly window'
  )
})

test('stripAriaLabel calls out stale and error providers in words, not color', () => {
  const usage = {
    providers: [
      { provider: 'claude', label: 'Claude', state: 'stale', primary: { label: '5h', remaining: 0.2 } },
      { provider: 'codex', label: 'Codex', state: 'error', error: 'timeout' },
    ],
  }
  assert.equal(
    stripAriaLabel(usage),
    'Provider usage: Claude usage stale; Codex usage unavailable'
  )
})

test('stripAriaLabel falls back to a bare label and title-cased provider id', () => {
  const usage = {
    providers: [
      { provider: 'claude', state: 'ok', primary: { remaining: 0.5 } },
    ],
  }
  assert.equal(stripAriaLabel(usage), 'Provider usage: Claude 50% remaining, window')
})

test('stripAriaLabel handles no providers', () => {
  assert.equal(stripAriaLabel({ providers: [] }), 'Provider usage')
  assert.equal(stripAriaLabel(null), 'Provider usage')
})
