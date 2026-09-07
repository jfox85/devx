import test from 'node:test'
import assert from 'node:assert/strict'
import { relativeHistoryTime } from './composerFormat.js'

test('relativeHistoryTime renders "just now" for a timestamp under a minute old', () => {
  const now = Date.parse('2026-09-07T12:00:00Z')
  assert.equal(relativeHistoryTime(now - 10_000, now), 'just now')
})

test('relativeHistoryTime buckets minutes, hours, and days', () => {
  const now = Date.parse('2026-09-07T12:00:00Z')
  assert.equal(relativeHistoryTime(now - 4 * 60_000, now), '4m ago')
  assert.equal(relativeHistoryTime(now - 2 * 3600_000, now), '2h ago')
  assert.equal(relativeHistoryTime(now - 3 * 86400_000, now), '3d ago')
})

test('relativeHistoryTime falls back to "just now" for invalid input', () => {
  assert.equal(relativeHistoryTime(NaN), 'just now')
  assert.equal(relativeHistoryTime(undefined), 'just now')
})
