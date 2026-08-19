import test from 'node:test'
import assert from 'node:assert/strict'
import { createTerminalAttempt, terminalFramePath } from './terminalActivity.js'

test('terminal attempts are opaque and frame URLs carry them', () => {
  const attempt = createTerminalAttempt({ randomUUID: () => '12345678-1234-1234-1234-123456789abc' })
  assert.equal(attempt, '12345678-1234-1234-1234-123456789abc')
  assert.equal(
    terminalFramePath('feature/demo', attempt),
    '/terminal/feature%2Fdemo/?devx_attempt=12345678-1234-1234-1234-123456789abc'
  )
})

test('terminal attempts fall back to secure random bytes without randomUUID', () => {
  const attempt = createTerminalAttempt({
    getRandomValues: bytes => {
      bytes.fill(0xab)
      return bytes
    },
  })
  assert.equal(attempt, 'ab'.repeat(16))
})

test('recreated frames receive distinct attempts', () => {
  let n = 0
  const crypto = { randomUUID: () => `00000000-0000-0000-0000-${String(++n).padStart(12, '0')}` }
  assert.notEqual(createTerminalAttempt(crypto), createTerminalAttempt(crypto))
})
