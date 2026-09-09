import test from 'node:test'
import assert from 'node:assert/strict'

// sessionUiState is an app-wide singleton around composerStorage; the module
// under test constructs createComposerStorage() at import time using
// globalThis.localStorage, so we install a fake localStorage before
// importing it (mirrors the real app's browser environment).
function makeFakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial))
  return {
    getItem: key => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => { map.set(key, String(value)) },
    removeItem: key => { map.delete(key) },
    get length() { return map.size },
    key(i) { return [...map.keys()][i] ?? null },
    _map: map,
  }
}

globalThis.localStorage = makeFakeStorage()
globalThis.window = globalThis.window || {}

const { getComposerDraft, setComposerDraft, flushComposerDrafts, getComposerPrefs, setComposerPrefs } = await import('./sessionUiState.js')

test('setComposerDraft/getComposerDraft round-trip through the real composer storage', () => {
  setComposerDraft('alpha', 'hello world')
  assert.equal(getComposerDraft('alpha'), 'hello world')
})

test('getComposerDraft returns empty string for an unknown session', () => {
  assert.equal(getComposerDraft('never-seen'), '')
})

test('setComposerDraft persists to localStorage once flushed, proving it is backed by composerStorage, not an in-memory Map', () => {
  setComposerDraft('persisted-session', 'durable text')
  flushComposerDrafts('persisted-session')
  const raw = globalThis.localStorage.getItem('devx_composer_v1:persisted-session')
  assert.ok(raw, 'expected the draft to be written to localStorage after flush')
  assert.equal(JSON.parse(raw).draft, 'durable text')
})

test('getComposerPrefs defaults both draft and history on, delegating to composerStorage', () => {
  assert.deepEqual(getComposerPrefs(), { draft: true, history: true })
})

test('setComposerPrefs updates and purges via composerStorage, and getComposerPrefs reflects the change', () => {
  setComposerDraft('prefs-purge-session', 'will be purged')
  flushComposerDrafts('prefs-purge-session')
  assert.ok(globalThis.localStorage.getItem('devx_composer_v1:prefs-purge-session'))

  const next = setComposerPrefs({ draft: false })
  assert.equal(next.draft, false)
  assert.equal(getComposerPrefs().draft, false)
  assert.equal(globalThis.localStorage.getItem('devx_composer_v1:prefs-purge-session'), null)

  setComposerPrefs({ draft: true })
  assert.equal(getComposerPrefs().draft, true)
})
