import test from 'node:test'
import assert from 'node:assert/strict'
import {
  createComposerStorage,
  mergeRecall,
  PREFS_KEY,
  sessionStorageKey,
  INDEX_KEY,
} from './composerStorage.js'

// --- test doubles ------------------------------------------------------------

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

function throwingStorage(message = 'blocked') {
  return {
    getItem: () => { throw new Error(message) },
    setItem: () => { throw new Error(message) },
    removeItem: () => { throw new Error(message) },
  }
}

function makeFakeTimers() {
  let nextId = 0
  const pending = new Map()
  return {
    setTimeout: (fn, ms) => {
      const id = ++nextId
      pending.set(id, fn)
      return id
    },
    clearTimeout: id => { pending.delete(id) },
    flushAll() {
      for (const [id, fn] of [...pending]) {
        pending.delete(id)
        fn()
      }
    },
    get pendingCount() { return pending.size },
  }
}

function makeLogger() {
  const calls = []
  return { warn: (...args) => calls.push(args), calls }
}

// --- 1. draft roundtrip + defensive clone -----------------------------------

test('draft roundtrip per session and defensive clone', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setDraft('alpha', 'hello world')
  const first = composer.getSession('alpha')
  assert.equal(first.draft, 'hello world')

  // Mutating the returned object must not affect internal state.
  first.draft = 'mutated'
  first.history.push({ text: 'x', at: 1 })
  const second = composer.getSession('alpha')
  assert.equal(second.draft, 'hello world')
  assert.deepEqual(second.history, [])

  timers.flushAll()
  const raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal(raw.draft, 'hello world')
})

test('empty draft text removes the persisted draft field but keeps history', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.recordSend('alpha', 'first send')
  composer.setDraft('alpha', 'typing…')
  timers.flushAll()
  let raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal(raw.draft, 'typing…')
  assert.equal(raw.history.length, 1)

  composer.setDraft('alpha', '')
  timers.flushAll()
  raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal('draft' in raw, false)
  assert.equal(raw.history.length, 1)
})

test('empty session names are rejected without throwing', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  composer.setDraft('', 'nope')
  assert.deepEqual(composer.getSession(''), { draft: '', history: [], sending: false })
  assert.equal(storage._map.size, 0)
})

// --- 2. independent per-session keys ----------------------------------------

test('two sessions use independent keys and writes cannot clobber each other', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setDraft('session-a', 'draft A')
  composer.setDraft('session with spaces', 'draft B')
  timers.flushAll()

  assert.notEqual(sessionStorageKey('session-a'), sessionStorageKey('session with spaces'))
  assert.equal(composer.getSession('session-a').draft, 'draft A')
  assert.equal(composer.getSession('session with spaces').draft, 'draft B')

  const rawA = JSON.parse(storage._map.get(sessionStorageKey('session-a')))
  const rawB = JSON.parse(storage._map.get(sessionStorageKey('session with spaces')))
  assert.equal(rawA.draft, 'draft A')
  assert.equal(rawB.draft, 'draft B')
})

// --- required scenario 2: cross-tab, different sessions, index integrity ---

test('two instances on shared storage writing different sessions do not clobber the index or each other\'s drafts', () => {
  const storage = makeFakeStorage()
  const a = createComposerStorage(storage)
  const b = createComposerStorage(storage) // simulates a second tab, constructed independently

  a.recordSend('sess-a', 'from tab a')
  b.recordSend('sess-b', 'from tab b')

  const index = JSON.parse(storage._map.get(INDEX_KEY))
  assert.equal('sess-a' in index, true, "tab b's index write must not drop tab a's entry")
  assert.equal('sess-b' in index, true)
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('sess-a'))).history[0].text, 'from tab a')
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('sess-b'))).history[0].text, 'from tab b')
})

test('pruneSessions removes an orphaned session key from another tab even when this instance never indexed it (M2)', () => {
  const storage = makeFakeStorage()
  const b = createComposerStorage(storage) // constructed first; caches an empty index view
  const a = createComposerStorage(storage)
  a.recordSend('orphan', 'hello from another tab')

  assert.equal(storage._map.has(sessionStorageKey('orphan')), true)

  // b's local index/sessions have no idea 'orphan' exists (loaded before a
  // wrote it), so only a raw-key prefix sweep can find and remove it.
  b.pruneSessions([]) // nothing is active

  assert.equal(storage._map.has(sessionStorageKey('orphan')), false, 'orphaned key must be swept by prefix even though b never indexed it')
})

// --- required scenario 3: same-session tabs must not lose a send -----------

test('a debounced draft write from one tab cannot erase a send recorded by another tab in the same session (M3)', () => {
  const storage = makeFakeStorage()
  const timersA = makeFakeTimers()
  const a = createComposerStorage(storage, { setTimeout: timersA.setTimeout, clearTimeout: timersA.clearTimeout })
  const b = createComposerStorage(storage)

  a.setDraft('shared', 'typing in tab a') // schedules a debounced write; not yet flushed
  b.recordSend('shared', 'sent from tab b') // immediate write from tab b

  timersA.flushAll() // tab a's debounce now fires and persists its draft

  const raw = JSON.parse(storage._map.get(sessionStorageKey('shared')))
  assert.equal(raw.draft, 'typing in tab a', 'last writer wins for the draft field')
  assert.equal(raw.history.length, 1, "tab a's draft write must not erase tab b's send")
  assert.equal(raw.history[0].text, 'sent from tab b')
})

// --- required scenario 1: reload restores draft + history -------------------

test('reload: a second instance over the same storage restores what the first instance persisted', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const a = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })
  a.setDraft('alpha', 'typed before reload')
  a.recordSend('alpha', 'sent before reload', 1)
  timers.flushAll()

  const b = createComposerStorage(storage) // simulates a fresh page load
  const restored = b.getSession('alpha')
  assert.equal(restored.draft, 'typed before reload')
  assert.deepEqual(restored.history, [{ text: 'sent before reload', at: 1 }])
})

// --- 3. debounce + flush/dispose ---------------------------------------------

test('setDraft debounces persistence; flush forces it synchronously', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout, debounceMs: 400 })

  composer.setDraft('alpha', 'partial')
  assert.equal(storage._map.has(sessionStorageKey('alpha')), false)
  assert.equal(timers.pendingCount, 1)

  composer.flush('alpha')
  assert.equal(timers.pendingCount, 0)
  const raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal(raw.draft, 'partial')
})

test('flush without a session name flushes all pending drafts', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setDraft('alpha', 'a-text')
  composer.setDraft('beta', 'b-text')
  composer.flush()
  assert.equal(timers.pendingCount, 0)
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('alpha'))).draft, 'a-text')
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('beta'))).draft, 'b-text')
})

// --- required scenario 9: dispose is terminal --------------------------------

test('dispose flushes pending drafts, clears timers, and is terminal: mutators no-op afterward', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setDraft('alpha', 'before dispose')
  composer.dispose()
  assert.equal(timers.pendingCount, 0, 'dispose must flush pending debounce timers')
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('alpha'))).draft, 'before dispose')

  composer.setDraft('alpha', 'after dispose - should be ignored')
  composer.recordSend('alpha', 'also ignored')
  composer.markSending('alpha', true)
  composer.clearDraft('alpha')
  composer.clearHistory('alpha')
  composer.setPrefs({ draft: false })
  composer.pruneSessions([])

  const state = composer.getSession('alpha')
  assert.equal(state.draft, 'before dispose')
  assert.equal(state.history.length, 0)
  assert.deepEqual(composer.getPrefs(), { draft: true, history: true })
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('alpha'))).draft, 'before dispose')
})

// --- required scenario 10: clearDraft immediate persistence -----------------

test('clearDraft immediately purges the persisted draft without waiting for the debounce (M-minor)', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.recordSend('alpha', 'sent')
  composer.setDraft('alpha', 'typed but now sent')
  composer.clearDraft('alpha')

  assert.equal(timers.pendingCount, 0)
  const raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal('draft' in raw, false)
  assert.equal(raw.history.length, 1)
  assert.equal(composer.getSession('alpha').draft, '')
})

// --- minor: re-enabling a pref persists in-memory state immediately --------

test('re-enabling a preference immediately persists the currently-known in-memory state', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setPrefs({ draft: false })
  composer.setDraft('alpha', 'typed while disabled') // memory-only, no timer scheduled
  assert.equal(timers.pendingCount, 0)

  composer.setPrefs({ draft: true })
  // No flush() call — re-enabling itself must persist immediately.
  const raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')) || '{}')
  assert.equal(raw.draft, 'typed while disabled')
})

// --- minor: trim commit only after successful write --------------------------

test('a storage failure during budget trimming does not destructively lose the untrimmed history entry (m-minor)', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, {
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    maxBytes: 10,
    maxSessions: 100,
  })

  composer.setDraft('alpha', 'keep me')
  timers.flushAll() // persists the draft while storage still works normally

  let calls = 0
  const originalSetItem = storage.setItem
  storage.setItem = (key, value) => {
    calls++
    if (calls <= 2) { originalSetItem(key, value); return } // send write + its index write succeed
    throw new Error('quota exceeded') // the budget-trim write that follows fails
  }

  composer.recordSend('alpha', 'must survive the failed trim attempt', 1)

  assert.equal(composer.isPersistent(), false, 'the failed write degrades the instance to memory-only')
  assert.equal(composer.getSession('alpha').history.length, 1, 'the entry must not be silently discarded when its persist attempt failed')
})

// --- 4. prefs defaults + purge ------------------------------------------------

test('prefs default both draft and history on; invalid stored JSON/types fall back to defaults', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  assert.deepEqual(composer.getPrefs(), { draft: true, history: true })

  storage.setItem(PREFS_KEY, 'not json')
  const other = createComposerStorage(storage) // fresh instance re-reads storage
  assert.deepEqual(other.getPrefs(), { draft: true, history: true })

  storage.setItem(PREFS_KEY, JSON.stringify({ draft: 'nope', history: 1 }))
  const third = createComposerStorage(storage)
  assert.deepEqual(third.getPrefs(), { draft: true, history: true })
})

test('disabling draft pref purges persisted drafts; memory-only draft still works', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setDraft('alpha', 'will be purged')
  timers.flushAll()
  assert.equal(JSON.parse(storage._map.get(sessionStorageKey('alpha'))).draft, 'will be purged')

  composer.setPrefs({ draft: false })
  assert.equal('draft' in (JSON.parse(storage._map.get(sessionStorageKey('alpha')) || '{}')), false)
  assert.deepEqual(composer.getPrefs(), { draft: false, history: true })

  // Memory-only draft continues to function for this page lifetime.
  composer.setDraft('alpha', 'typed after disabling')
  assert.equal(composer.getSession('alpha').draft, 'typed after disabling')
  assert.equal(timers.pendingCount, 0) // no persistence scheduled while pref is off
  timers.flushAll()
  assert.equal('draft' in (JSON.parse(storage._map.get(sessionStorageKey('alpha')) || '{}')), false)
})

test('disabling history pref purges stored history for all sessions', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  composer.recordSend('alpha', 'first')
  composer.recordSend('beta', 'second')
  assert.equal(composer.getSession('alpha').history.length, 1)

  composer.setPrefs({ history: false })
  assert.equal(composer.getSession('alpha').history.length, 0)
  assert.equal(composer.getSession('beta').history.length, 0)
  assert.equal('history' in JSON.parse(storage._map.get(sessionStorageKey('alpha')) || '{}'), false)

  composer.recordSend('alpha', 'ignored while disabled')
  assert.equal(composer.getSession('alpha').history.length, 0)
})

// --- required scenario 8: prefs off gates + purges old/other-tab data (M4) --

test('prefs off gates loaded draft/history and purges stale stored data from an old build/other tab (M4)', () => {
  const storage = makeFakeStorage()
  // Simulate data written by an older build or another tab while prefs were on.
  storage.setItem(sessionStorageKey('legacy'), JSON.stringify({
    draft: 'left over draft',
    history: [{ text: 'left over send', at: 1 }],
  }))
  storage.setItem(INDEX_KEY, JSON.stringify({ legacy: 1 }))
  storage.setItem(PREFS_KEY, JSON.stringify({ draft: false, history: false }))

  const composer = createComposerStorage(storage) // constructed AFTER prefs disabled + storage prepopulated

  const state = composer.getSession('legacy')
  assert.equal(state.draft, '', 'draft must be gated off by current prefs')
  assert.equal(state.history.length, 0, 'history must be gated off by current prefs')

  const raw = JSON.parse(storage._map.get(sessionStorageKey('legacy')) || '{}')
  assert.equal('draft' in raw, false, 'stale draft must be purged from storage on construction')
  assert.equal('history' in raw, false, 'stale history must be purged from storage on construction')
})

// --- cross-tab prefs purge: a stale cached pref must never resurrect data ---
// another tab already purged from disk (fix for SECURITY MEDIUM cross-tab
// prefs finding: prefs must be re-read from storage at every persisting
// write boundary, not just cached from construction/last local getPrefs()).

test('B.recordSend must not resurrect persisted history after A disables history and purges (cross-tab)', () => {
  const storage = makeFakeStorage()
  const a = createComposerStorage(storage)
  const b = createComposerStorage(storage)

  // Both instances cache prefs as {draft:true, history:true} before A changes anything.
  assert.deepEqual(a.getPrefs(), { draft: true, history: true })
  assert.deepEqual(b.getPrefs(), { draft: true, history: true })

  a.recordSend('shared', 'sent before disable')
  a.setPrefs({ history: false }) // purges persisted history on disk

  b.recordSend('shared', 'attempted after disable') // b's cached prefs are stale (still history:true)

  const raw = JSON.parse(storage._map.get(sessionStorageKey('shared')) || '{}')
  assert.equal('history' in raw, false, "B's write must not resurrect persisted history after A disabled it")
})

test('B setDraft/flush must not persist a draft after A disables the draft pref and purges (cross-tab)', () => {
  const storage = makeFakeStorage()
  const timersB = makeFakeTimers()
  const a = createComposerStorage(storage)
  const b = createComposerStorage(storage, { setTimeout: timersB.setTimeout, clearTimeout: timersB.clearTimeout })

  assert.deepEqual(a.getPrefs(), { draft: true, history: true })
  assert.deepEqual(b.getPrefs(), { draft: true, history: true })

  a.setDraft('shared', 'a typed something')
  a.flush('shared')
  a.setPrefs({ draft: false }) // purges persisted draft on disk

  b.setDraft('shared', 'b typed after disable') // b's cached prefs are stale (still draft:true)
  b.flush('shared')

  const raw = JSON.parse(storage._map.get(sessionStorageKey('shared')) || '{}')
  assert.equal('draft' in raw, false, "B's flush must not persist a draft after A disabled the draft pref")
})

// --- 5. history ordering, dedupe, cap ----------------------------------------

test('recordSend prepends newest-first and collapses immediate consecutive duplicates', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage, { now: () => 1000 })
  composer.recordSend('alpha', 'one', 1)
  composer.recordSend('alpha', 'two', 2)
  composer.recordSend('alpha', 'two', 3) // duplicate of most recent -> replace timestamp only
  composer.recordSend('alpha', 'one', 4) // not consecutive with 'two' -> new entry

  const { history } = composer.getSession('alpha')
  assert.deepEqual(history, [
    { text: 'one', at: 4 },
    { text: 'two', at: 3 },
    { text: 'one', at: 1 },
  ])
})

test('history caps at 20 entries', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  for (let i = 0; i < 25; i++) composer.recordSend('alpha', `msg-${i}`, i)
  const { history } = composer.getSession('alpha')
  assert.equal(history.length, 20)
  assert.equal(history[0].text, 'msg-24')
  assert.equal(history[19].text, 'msg-5')
})

test('clearHistory empties a single session history', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  composer.recordSend('alpha', 'one')
  composer.recordSend('beta', 'two')
  composer.clearHistory('alpha')
  assert.equal(composer.getSession('alpha').history.length, 0)
  assert.equal(composer.getSession('beta').history.length, 1)
})

test('clearHistory is authoritative: it stays empty even when this instance\'s cached state predates another tab\'s send (cross-tab policy)', () => {
  const storage = makeFakeStorage()
  const a = createComposerStorage(storage)
  const b = createComposerStorage(storage)

  b.getSession('shared') // b loads and caches an empty session before any send exists
  a.recordSend('shared', 'sent from tab a')

  b.clearHistory('shared') // must not merge in a's just-recorded send

  const raw = JSON.parse(storage._map.get(sessionStorageKey('shared')) || '{}')
  assert.equal('history' in raw, false, "clearHistory must not resurrect another tab's send via a disk merge")
  assert.equal(b.getSession('shared').history.length, 0)
})

// --- 6. mergeRecall (M6: pure function, no longer instance policy) ----------

test('mergeRecall replaces empty text, appends on a new line otherwise', () => {
  assert.equal(mergeRecall('', 'recall me'), 'recall me')
  assert.equal(mergeRecall('typing now', 'recall me'), 'typing now\nrecall me')
  assert.equal(mergeRecall('typing now', ''), 'typing now')
  assert.equal(mergeRecall('', ''), '')
})

test('composer storage no longer exposes UI recall policy (M6)', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  assert.equal('recall' in composer, false)
})

// --- 7. markSending immediate persistence ------------------------------------

test('markSending persists immediately and retains draft/history', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  composer.setDraft('alpha', 'in flight')
  composer.recordSend('alpha', 'prior send', 1)
  composer.markSending('alpha', true)

  // markSending is immediate, unlike the debounced draft write, but should
  // include the still-pending draft value since it reads current in-memory state.
  const raw = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal(raw.sending, true)
  assert.equal(raw.draft, 'in flight')
  assert.equal(raw.history.length, 1)

  composer.markSending('alpha', false)
  const raw2 = JSON.parse(storage._map.get(sessionStorageKey('alpha')))
  assert.equal('sending' in raw2, false)
})

// --- 8. storage failure resilience -------------------------------------------

test('storage that throws on every access degrades to memory-only and logs once', () => {
  const storage = throwingStorage()
  const logger = makeLogger()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { logger, setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  assert.doesNotThrow(() => {
    composer.setDraft('alpha', 'still works')
    timers.flushAll()
    composer.markSending('alpha', true)
    composer.recordSend('alpha', 'sent anyway')
    composer.getPrefs()
    composer.setPrefs({ draft: false })
    composer.flush('alpha')
    composer.pruneSessions(['alpha'])
    composer.dispose()
  })

  assert.equal(composer.getSession('alpha').draft, 'still works')
  assert.equal(composer.isPersistent(), false)
  assert.equal(logger.calls.length, 1)
})

test('an instance with no storage available runs memory-only without logging a failure', () => {
  const logger = makeLogger()
  const composer = createComposerStorage(undefined, { logger })
  assert.equal(composer.isPersistent(), false)
  composer.setDraft('alpha', 'memory only')
  assert.equal(composer.getSession('alpha').draft, 'memory only')
  assert.equal(logger.calls.length, 0)
})

// --- 9. pruneSessions ----------------------------------------------------------

test('pruneSessions removes storage entries and index entries for inactive sessions', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  composer.recordSend('keep-me', 'a')
  composer.recordSend('drop-me', 'b')

  composer.pruneSessions(['keep-me'])

  assert.equal(storage._map.has(sessionStorageKey('drop-me')), false)
  assert.equal(storage._map.has(sessionStorageKey('keep-me')), true)
  assert.deepEqual(composer.getSession('drop-me'), { draft: '', history: [], sending: false })

  const index = JSON.parse(storage._map.get(INDEX_KEY))
  assert.equal('drop-me' in index, false)
  assert.equal('keep-me' in index, true)

  // Prefs key itself must never be purged by pruneSessions.
  assert.equal(storage._map.has(PREFS_KEY), false) // never written yet, but pruning must not create/touch it either
})

// --- 10. caps: 40 sessions, never evict drafts, byte budget -------------------

test('caps total sessions at 40 and evicts the oldest session without a draft first', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage, { now: () => Date.now(), maxSessions: 40 })

  for (let i = 0; i < 40; i++) composer.recordSend(`session-${i}`, `msg-${i}`, i)
  // 40 sessions exist, none with drafts yet.
  let index = JSON.parse(storage._map.get(INDEX_KEY))
  assert.equal(Object.keys(index).length, 40)

  composer.recordSend('session-40', 'msg-40', 40)
  index = JSON.parse(storage._map.get(INDEX_KEY))
  assert.equal(Object.keys(index).length, 40)
  assert.equal('session-0' in index, false) // oldest evicted
  assert.equal('session-40' in index, true)
})

test('never evicts a session holding a non-empty draft even when over the session cap', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, {
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    maxSessions: 2,
  })

  composer.setDraft('has-draft', 'keep me around')
  timers.flushAll()
  composer.recordSend('no-draft', 'x', 1)
  // Adding a third session should evict 'no-draft' (empty draft), never 'has-draft'.
  composer.recordSend('another', 'y', 2)

  assert.equal(composer.getSession('has-draft').draft, 'keep me around')
  const index = JSON.parse(storage._map.get(INDEX_KEY))
  assert.equal('has-draft' in index, true)
})

// --- required scenario 4: true LRU with interleaved use + numeric name (H1) -

test('LRU (not FIFO) evicts by last-write recency, including a numeric session name (H1/H3)', () => {
  const storage = makeFakeStorage()
  let clock = 0
  const composer = createComposerStorage(storage, { maxSessions: 3, now: () => ++clock })

  composer.recordSend('1', 'a')       // created @1 — numeric name to catch object-key reordering bugs
  composer.recordSend('alpha', 'b')   // created @2
  composer.recordSend('beta', 'c')    // created @3
  // Touch '1' again so it becomes most-recently-used even though it was created first.
  composer.recordSend('1', 'd')       // last-write @4 for '1'

  // Adding a 4th session must evict 'alpha' (least-recently-used), not '1'
  // (which FIFO-by-creation-order would incorrectly evict first).
  composer.recordSend('gamma', 'e')

  const index = JSON.parse(storage._map.get(INDEX_KEY))
  assert.equal('1' in index, true, 'recently-touched numeric-named session must survive')
  assert.equal('alpha' in index, false, 'least-recently-used session must be evicted, not the numerically-first key')
  assert.equal('beta' in index, true)
  assert.equal('gamma' in index, true)
})

test('enforces an injected byte budget by trimming the least-recently-used session first, with concrete survivors (H2/H3)', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  let clock = 0
  const composer = createComposerStorage(storage, {
    setTimeout: timers.setTimeout,
    clearTimeout: timers.clearTimeout,
    now: () => ++clock,
    maxBytes: 900,
    maxSessions: 100,
  })

  // s1 is created first but re-touched last -> becomes most-recently-used.
  composer.setDraft('s1', 'draft for s1'); timers.flushAll()
  composer.recordSend('s1', 's1-message-one')
  composer.setDraft('s2', 'draft for s2'); timers.flushAll()
  composer.recordSend('s2', 's2-message-one')
  composer.setDraft('s3', 'draft for s3'); timers.flushAll()
  composer.recordSend('s3', 's3-message-one')
  composer.recordSend('s1', 's1-message-two') // re-touch s1: newest last-write of the three

  // All three have drafts, so eviction can't remove a whole session; budget
  // enforcement must fall back to trimming history, oldest-by-last-write first.
  for (let i = 0; i < 10; i++) composer.recordSend('s3', `s3-filler-${i}`)

  assert.ok(composer.getSession('s1').draft.length > 0, 's1 draft must survive')
  assert.ok(composer.getSession('s2').draft.length > 0, 's2 draft must survive')
  assert.ok(composer.getSession('s3').draft.length > 0, 's3 draft must survive')

  const s1History = composer.getSession('s1').history.length
  const s2History = composer.getSession('s2').history.length
  assert.ok(s2History <= s1History, `expected LRU session s2 (${s2History}) to be trimmed at or below recently-used s1 (${s1History})`)
  assert.ok(s1History > 0, 's1 (most recently used) must retain at least one history entry')

  let totalBytes = 0
  for (const name of ['s1', 's2', 's3']) {
    const raw = storage._map.get(sessionStorageKey(name))
    if (raw) totalBytes += Buffer.byteLength(raw, 'utf8')
  }
  assert.ok(totalBytes <= 900, `expected total bytes <= 900, got ${totalBytes}`)
})

// --- required scenario 5: byte budget accounts for reload (M1) --------------

test('byte budget accounts for pre-existing persisted sessions immediately after reload (M1)', () => {
  const storage = makeFakeStorage()
  const a = createComposerStorage(storage, { maxSessions: 100 })
  a.setDraft('alpha', 'a persisted draft'); a.flush('alpha')
  for (let i = 0; i < 10; i++) a.recordSend('alpha', `existing-message-${i}`, i)

  const sizeBefore = Buffer.byteLength(storage._map.get(sessionStorageKey('alpha')), 'utf8')
  assert.ok(sizeBefore > 50, 'sanity: alpha has a meaningful persisted size')

  // A brand-new instance (reload) with a byte budget smaller than what's
  // already on disk must trim on its very first write, proving it accounted
  // for pre-existing bytes rather than starting the budget at 0.
  const b = createComposerStorage(storage, { maxBytes: sizeBefore - 10, maxSessions: 100 })
  b.recordSend('beta', 'triggers budget check')

  const alphaHistoryAfter = b.getSession('alpha').history.length
  assert.ok(alphaHistoryAfter < 10, 'pre-existing alpha history must be trimmed once the budget is exceeded on reload')
})

// --- required scenario 6: oversized draft capped, unrelated history survives (H2) -

test('a 600KB pasted draft is capped at 64KB and does not consume future unrelated history budget (H2)', () => {
  const storage = makeFakeStorage()
  const timers = makeFakeTimers()
  const composer = createComposerStorage(storage, { setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout })

  const huge = 'a'.repeat(600 * 1024)
  composer.setDraft('big', huge)
  timers.flushAll()

  const cappedDraft = composer.getSession('big').draft
  assert.ok(Buffer.byteLength(cappedDraft, 'utf8') <= 64 * 1024, 'draft must be capped at 64KB')
  assert.ok(cappedDraft.endsWith('… [truncated]'))

  // An unrelated session must still be able to accumulate history afterward.
  for (let i = 0; i < 5; i++) composer.recordSend('other', `message-${i}`, i)
  assert.equal(composer.getSession('other').history.length, 5, 'unrelated session history must survive a previously oversized draft')
})

// --- required scenario 7: over-cap corrupt persisted state sanitized (M5) --

test('corrupt/over-cap persisted state is sanitized on load: history capped, entries truncated, invalid records dropped (M5)', () => {
  const storage = makeFakeStorage()
  const longText = 'x'.repeat(5000)
  const rawHistory = []
  for (let i = 0; i < 5000; i++) rawHistory.push({ text: longText, at: i })
  rawHistory.push({ text: 123, at: 1 })          // invalid text type -> dropped
  rawHistory.push({ text: 'no timestamp' })       // missing at -> dropped
  rawHistory.push({ text: 'nan at', at: NaN })    // non-finite at -> dropped
  storage.setItem(sessionStorageKey('corrupt'), JSON.stringify({
    draft: 'x'.repeat(200000), // way over 64KB
    history: rawHistory,
    sending: 'not-a-boolean',
  }))
  storage.setItem(INDEX_KEY, JSON.stringify({ corrupt: 1 }))

  const composer = createComposerStorage(storage)
  const state = composer.getSession('corrupt')

  assert.equal(state.history.length, 20, 'history must be capped at 20 on load')
  for (const entry of state.history) {
    assert.ok(Buffer.byteLength(entry.text, 'utf8') <= 2048, 'each entry must be capped at 2KB on load')
  }
  assert.ok(Buffer.byteLength(state.draft, 'utf8') <= 64 * 1024, 'draft must be capped at 64KB on load')
  assert.equal(state.sending, false, 'invalid sending flag falls back to false')
})

// --- unicode-safe 2KB truncation ----------------------------------------------

test('history entries longer than 2KB (UTF-8) truncate without splitting unicode and add a suffix', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  const emoji = '😀' // 4 bytes in UTF-8, a surrogate pair in UTF-16
  const longText = emoji.repeat(1000) // ~4000 bytes, well over the 2KB cap
  composer.recordSend('alpha', longText)

  const { history } = composer.getSession('alpha')
  const stored = history[0].text
  assert.ok(stored.endsWith('… [truncated]'))
  assert.ok(Buffer.byteLength(stored, 'utf8') <= 2048)
  // No lone surrogate / replacement character from splitting a code point.
  assert.equal(/\uFFFD/.test(stored), false)
  for (const ch of stored.replace('… [truncated]', '')) {
    assert.equal(ch, emoji)
  }
})

test('text under the 2KB cap is stored unchanged', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage)
  composer.recordSend('alpha', 'short text')
  assert.equal(composer.getSession('alpha').history[0].text, 'short text')
})

// --- injectable now for recordSend default ------------------------------------

test('recordSend uses injected now() when at is omitted', () => {
  const storage = makeFakeStorage()
  const composer = createComposerStorage(storage, { now: () => 12345 })
  composer.recordSend('alpha', 'text')
  assert.equal(composer.getSession('alpha').history[0].at, 12345)
})
