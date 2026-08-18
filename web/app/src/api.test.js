import test from 'node:test'
import assert from 'node:assert/strict'
import { recordSessionActivity } from './api.js'

test('activity receipt is not consumed after the frame becomes stale', async () => {
  const requests = []
  globalThis.window = { location: { reload() {} }, localStorage: { removeItem() {} } }
  globalThis.fetch = async (url) => {
    requests.push(url)
    return { ok: true, status: 200, json: async () => ({ receipt: 'one-time' }) }
  }

  const recorded = await recordSessionActivity('alpha', 'attempt-12345678', () => false)
  assert.equal(recorded, false)
  assert.deepEqual(requests, ['/api/terminal/activity-receipt'])
})

test('current frame consumes its one-time activity receipt', async () => {
  const requests = []
  globalThis.fetch = async (url) => {
    requests.push(url)
    if (url.endsWith('activity-receipt')) {
      return { ok: true, status: 200, json: async () => ({ receipt: 'one-time' }) }
    }
    return { ok: true, status: 204, json: async () => ({}) }
  }

  const recorded = await recordSessionActivity('alpha', 'attempt-12345678', () => true)
  assert.equal(recorded, true)
  assert.deepEqual(requests, ['/api/terminal/activity-receipt', '/api/sessions/activity'])
})
