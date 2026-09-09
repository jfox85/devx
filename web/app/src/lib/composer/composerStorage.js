// Composer draft + prompt-history storage.
//
// Policy (see docs/plans/2026-09-05-composer-draft-persistence-and-history.md):
//  - One localStorage key per session (`devx_composer_v1:<encoded name>`) so two
//    tabs on different sessions never clobber each other's debounced writes.
//  - A small index key (`devx_composer_v1__index`) records session -> last
//    successful-write time, used for true LRU bookkeeping (not creation
//    order). It is re-read and merged before every write, since two tabs can
//    write it independently.
//  - Everything is dependency-injected (storage, timers, clock, logger) so the
//    module has no permanent module-global state and is deterministic in tests.
//  - Every storage access is wrapped; the first failure (quota, private mode,
//    storage absent) permanently degrades this instance to memory-only,
//    without throwing and without repeating the log line on every call after.
//  - Drafts remain usable in memory even when the draft preference is off or
//    storage is unavailable, for the current page lifetime only.
//  - Two write policies, both funneled through named helpers below:
//      persistMerged(name, state)    — ordinary user-activity writes (debounced
//        draft persistence, clearDraft, markSending, recordSend). Always
//        unions this write's history with whatever is currently on disk, so a
//        write from this tab can never silently erase a send another tab
//        already recorded for the same session.
//      persistOverwrite(name, state) — explicit destructive actions
//        (clearHistory, pref purge). Writes this instance's state as-is, with
//        no merge: "clear" is authoritative and must not be resurrected by a
//        stale on-disk copy from another tab.
//  - Preferences are re-read from storage at every persisting write boundary
//    (not on every get/read) so another tab disabling a pref takes effect on
//    this instance's very next write, before any data can be written under a
//    stale cached preference. This module has no window/storage-event
//    listener; reconciliation happens lazily at the write boundary instead.

export const PREFS_KEY = 'devx_composer_prefs_v1'
export const INDEX_KEY = 'devx_composer_v1__index'
const SESSION_KEY_PREFIX = 'devx_composer_v1:'

const HISTORY_MAX = 20
const ENTRY_MAX_BYTES = 2048
const DRAFT_MAX_BYTES = 64 * 1024
const DEFAULT_MAX_SESSIONS = 40
const DEFAULT_MAX_BYTES = 512 * 1024
const DEFAULT_DEBOUNCE_MS = 400
const TRUNCATE_SUFFIX = '… [truncated]'

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder('utf-8', { fatal: false })

function byteLength(text) {
  return textEncoder.encode(text).length
}

// Truncate `text` to at most `maxBytes` UTF-8 bytes (including the suffix),
// never splitting a multi-byte code point. Text within budget is untouched.
export function truncateUtf8(text, maxBytes = ENTRY_MAX_BYTES) {
  const bytes = textEncoder.encode(text)
  if (bytes.length <= maxBytes) return text
  const suffixBytes = textEncoder.encode(TRUNCATE_SUFFIX)
  let end = Math.max(0, maxBytes - suffixBytes.length)
  // Continuation bytes match 10xxxxxx (0x80..0xBF); back up until `end` sits
  // on a code-point boundary rather than mid-sequence.
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--
  return textDecoder.decode(bytes.slice(0, end)) + TRUNCATE_SUFFIX
}

export function sessionStorageKey(sessionName) {
  return `${SESSION_KEY_PREFIX}${encodeURIComponent(sessionName)}`
}

function decodeSessionKey(storageKey) {
  if (!storageKey.startsWith(SESSION_KEY_PREFIX)) return null
  try {
    return decodeURIComponent(storageKey.slice(SESSION_KEY_PREFIX.length))
  } catch {
    return null
  }
}

function normalizeName(sessionName) {
  return typeof sessionName === 'string' && sessionName !== '' ? sessionName : ''
}

function emptySessionState() {
  return { draft: '', history: [], sending: false }
}

function cloneSessionState(state) {
  return {
    draft: state.draft,
    history: state.history.map(entry => ({ text: entry.text, at: entry.at })),
    sending: state.sending,
  }
}

// Pure UI policy: how a recalled history entry combines with whatever is
// currently typed in the composer box. Lives outside the storage engine so
// this module stays purely about persistence; the caller indexes its own
// `getSession(name).history` array to pick an entry.
export function mergeRecall(currentText, entryText) {
  const current = typeof currentText === 'string' ? currentText : ''
  const entry = typeof entryText === 'string' ? entryText : ''
  if (!entry) return current
  return current === '' ? entry : `${current}\n${entry}`
}

// Sanitize a parsed session blob against every storage invariant, whether it
// came from this build's own writes, an older build, or another tab: history
// capped at HISTORY_MAX, each entry's text capped at ENTRY_MAX_BYTES, draft
// capped at DRAFT_MAX_BYTES, timestamps must be finite numbers, and malformed
// records are dropped rather than propagated.
function sanitizeSessionState(parsed) {
  const rawDraft = typeof parsed?.draft === 'string' ? parsed.draft : ''
  const draft = truncateUtf8(rawDraft, DRAFT_MAX_BYTES)
  const history = Array.isArray(parsed?.history)
    ? parsed.history
      .filter(entry => entry && typeof entry.text === 'string' && typeof entry.at === 'number' && Number.isFinite(entry.at))
      .map(entry => ({ text: truncateUtf8(entry.text, ENTRY_MAX_BYTES), at: entry.at }))
      .slice(0, HISTORY_MAX)
    : []
  const sending = typeof parsed?.sending === 'boolean' ? parsed.sending : false
  return { draft, history, sending }
}

function sanitizePrefs(parsed) {
  const draft = typeof parsed?.draft === 'boolean' ? parsed.draft : true
  const history = typeof parsed?.history === 'boolean' ? parsed.history : true
  return { draft, history }
}

export function createComposerStorage(storage = globalThis.localStorage, options = {}) {
  const {
    logger = console,
    setTimeout: scheduleTimeout = globalThis.setTimeout,
    clearTimeout: cancelTimeout = globalThis.clearTimeout,
    debounceMs = DEFAULT_DEBOUNCE_MS,
    now: nowFn = Date.now,
    maxSessions = DEFAULT_MAX_SESSIONS,
    maxBytes = DEFAULT_MAX_BYTES,
  } = options

  // Flips to false permanently on the first storage exception (or if no
  // storage was supplied at all). Once false, every guarded call below is a
  // free no-op — this is what keeps get/set/record working purely in memory
  // after a quota/private-mode failure, without ever throwing again.
  let persistent = !!storage
  let loggedFailure = false

  // Terminal flag: once dispose() runs, every mutator becomes a no-op. Reads
  // still return whatever was in memory at the time of disposal.
  let disposed = false

  // In-memory prefs cache, populated on first read and kept current by
  // setPrefs. Avoids a getItem+JSON.parse on every write (recordSend calls it
  // at least twice per send otherwise) and is also what memory-only mode uses
  // to make a preference change "stick" without storage to read it back from.
  let cachedPrefs = null

  const sessions = new Map() // sessionName -> live session state (canonical in-memory truth)
  const pendingTimers = new Map() // sessionName -> debounce timer id
  let index = new Map() // sessionName -> last-successful-write timestamp (true LRU order)
  let indexLoaded = false

  function handleFailure(err) {
    if (!persistent) return
    persistent = false
    if (!loggedFailure) {
      loggedFailure = true
      try { logger.warn('composerStorage: storage unavailable, falling back to memory-only', err) } catch { /* logging must never throw */ }
    }
  }

  function guarded(fn, fallback) {
    if (!persistent) return fallback
    try {
      return fn()
    } catch (err) {
      handleFailure(err)
      return fallback
    }
  }

  const safeGet = key => guarded(() => storage.getItem(key), null)
  const safeSet = (key, value) => guarded(() => { storage.setItem(key, value); return true }, false)
  const safeRemove = key => guarded(() => { storage.removeItem(key); return true }, false)

  // Parse a raw index blob (object of name -> timestamp) into a Map. A plain
  // object was rejected here on purpose: JS engines enumerate integer-like
  // string keys ("1", "42") in ascending numeric order ahead of insertion
  // order, which silently corrupts LRU ordering for numeric session names.
  // A Map preserves true insertion/update order for every key type.
  function parseIndexBlob(raw) {
    const map = new Map()
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return map
    for (const [name, at] of Object.entries(raw)) {
      if (typeof at === 'number' && Number.isFinite(at)) map.set(name, at)
    }
    return map
  }

  function loadIndexFromStorage() {
    const raw = safeGet(INDEX_KEY)
    if (!raw) return new Map()
    try {
      return parseIndexBlob(JSON.parse(raw))
    } catch {
      return new Map() // corrupt index — start fresh
    }
  }

  function ensureIndexLoaded() {
    if (indexLoaded) return
    indexLoaded = true
    index = loadIndexFromStorage()
  }

  // Re-reads the persisted index and merges it with the in-memory one before
  // writing, so a second tab's index write (built from its own possibly-stale
  // view) can never drop this tab's entries. Newer timestamp wins per
  // session on conflict.
  function persistIndex(excludeName) {
    const onDisk = loadIndexFromStorage()
    for (const [name, at] of onDisk) {
      if (name === excludeName) continue // a deletion in progress for this name must not be undone by a stale on-disk copy
      const current = index.get(name)
      if (current === undefined || at > current) index.set(name, at)
    }
    const obj = {}
    for (const [name, at] of index) obj[name] = at
    safeSet(INDEX_KEY, JSON.stringify(obj))
  }

  // Sweep raw storage keys by prefix for the actual set of persisted session
  // keys. Needed because the index can, in principle, still miss a key this
  // instance never learned about (e.g. it loaded before another tab wrote
  // it); pruneSessions must not orphan such a key.
  function listPersistedSessionNames() {
    const names = new Set()
    guarded(() => {
      const length = storage.length
      if (typeof length !== 'number' || typeof storage.key !== 'function') return
      for (let i = 0; i < length; i++) {
        const key = storage.key(i)
        if (typeof key !== 'string') continue
        const name = decodeSessionKey(key)
        if (name !== null) names.add(name)
      }
    }, undefined)
    return names
  }

  function allKnownNames() {
    ensureIndexLoaded()
    const names = new Set([...index.keys(), ...sessions.keys(), ...listPersistedSessionNames()])
    return names
  }

  function readPrefsFromStorage() {
    const raw = safeGet(PREFS_KEY)
    if (!raw) return { draft: true, history: true }
    try {
      return sanitizePrefs(JSON.parse(raw))
    } catch {
      return { draft: true, history: true }
    }
  }

  function getPrefs() {
    if (cachedPrefs) return { ...cachedPrefs }
    cachedPrefs = persistent ? readPrefsFromStorage() : { draft: true, history: true }
    return { ...cachedPrefs }
  }

  // Called only at persisting write boundaries (writeSessionRaw), never on a
  // plain get/read: re-reads prefs from storage and reconciles cachedPrefs to
  // match, so a preference another tab already flipped off (and purged) is
  // gated on this instance's very next write rather than resurrected by a
  // stale cached value. Once degraded to memory-only there is no disk to
  // reconcile against, so this just returns the in-memory cache.
  function refreshPrefsForWrite() {
    if (!persistent) return getPrefs()
    cachedPrefs = readPrefsFromStorage()
    return { ...cachedPrefs }
  }

  function loadSessionState(sessionName) {
    if (sessions.has(sessionName)) return sessions.get(sessionName)
    let state = emptySessionState()
    const raw = safeGet(sessionStorageKey(sessionName))
    if (raw) {
      try {
        const sanitized = sanitizeSessionState(JSON.parse(raw))
        const prefs = getPrefs()
        // Gate loaded fields on current prefs immediately: data left by an
        // older build, another tab, or before a pref was disabled must never
        // be handed back to the UI just because it's still on disk.
        state = {
          draft: prefs.draft ? sanitized.draft : '',
          history: prefs.history ? sanitized.history : [],
          sending: sanitized.sending,
        }
      } catch { /* corrupt entry — treat as absent */ }
    }
    sessions.set(sessionName, state)
    return state
  }

  function cancelPendingTimer(sessionName) {
    const id = pendingTimers.get(sessionName)
    if (id === undefined) return
    pendingTimers.delete(sessionName)
    try { cancelTimeout(id) } catch { /* injected timer already fired/cleared */ }
  }

  function removePersistedSession(sessionName) {
    safeRemove(sessionStorageKey(sessionName))
    ensureIndexLoaded()
    if (index.has(sessionName)) {
      index.delete(sessionName)
      persistIndex(sessionName)
    }
  }

  function forgetSession(sessionName) {
    cancelPendingTimer(sessionName)
    removePersistedSession(sessionName)
    sessions.delete(sessionName)
  }

  // O(<=maxSessions) scan over the currently-indexed keys' persisted JSON
  // size, used to (re)establish the byte budget without an incrementally
  // maintained cache that has to be kept in perfect sync across every write
  // and removal path. Cheap given the 40-session cap and only ever run when
  // actually checking the budget (simplicity review M1 suggestion).
  function currentTotalBytes() {
    ensureIndexLoaded()
    let total = 0
    for (const name of index.keys()) {
      const raw = safeGet(sessionStorageKey(name))
      if (raw) total += byteLength(raw)
    }
    return total
  }

  // Writes the current in-memory state for `sessionName`, gating draft/history
  // fields on the live preferences so a purge (setPrefs turning a field off)
  // takes effect on the very next write without a separate sweep, and so a
  // memory-only draft (pref off) is simply never included on disk. Returns
  // whether the write (or resulting removal) succeeded, so callers can avoid
  // committing an in-memory mutation whose persistence failed.
  function writeSessionRaw(sessionName, state) {
    const prefs = refreshPrefsForWrite()
    const obj = {}
    if (prefs.draft && state.draft) obj.draft = state.draft
    if (prefs.history && state.history.length) obj.history = state.history
    if (state.sending) obj.sending = true

    if (!('draft' in obj) && !('history' in obj) && !('sending' in obj)) {
      removePersistedSession(sessionName)
      return true
    }
    const json = JSON.stringify(obj)
    const ok = safeSet(sessionStorageKey(sessionName), json)
    if (!ok) return false
    ensureIndexLoaded()
    index.set(sessionName, nowFn()) // every successful write refreshes recency
    persistIndex()
    return true
  }

  // Sessions ordered oldest-write-first (true LRU, not creation/insertion
  // order), excluding any with a non-empty draft — those must never be
  // evicted regardless of caps/budget.
  function evictionCandidates() {
    ensureIndexLoaded()
    return [...index.entries()]
      .sort((a, b) => a[1] - b[1])
      .map(([name]) => name)
      .filter(name => !loadSessionState(name).draft)
  }

  function enforceSessionCap() {
    if (!persistent) return // caps no longer apply once degraded to memory-only
    ensureIndexLoaded()
    while (index.size > maxSessions) {
      const [victim] = evictionCandidates()
      if (!victim) break // everyone left has a draft — allow exceeding the cap
      forgetSession(victim)
    }
  }

  function enforceByteBudget() {
    if (!persistent) return // caps no longer apply once degraded to memory-only
    let total = currentTotalBytes()
    while (total > maxBytes) {
      const before = total
      const [victim] = evictionCandidates()
      if (victim) {
        forgetSession(victim)
        total = currentTotalBytes()
        if (total < before) continue
        break // a full pass made no progress — stop rather than loop forever
      }
      // Every remaining session has a draft (never evicted). Fall back to
      // trimming the oldest-by-last-write session's oldest history entry
      // (arrays are newest-first, so the oldest entry is the last one) until
      // we're back under budget. The in-memory trim is only committed after
      // a successful write, so a failed persist never destructively loses
      // the untrimmed entry.
      ensureIndexLoaded()
      const ordered = [...index.entries()].sort((a, b) => a[1] - b[1]).map(([name]) => name)
      const trimName = ordered.find(name => loadSessionState(name).history.length > 0)
      if (!trimName) break // nothing left to trim; budget stays over (best effort)
      const state = sessions.get(trimName)
      const candidate = { ...state, history: state.history.slice(0, -1) }
      if (!writeSessionRaw(trimName, candidate)) {
        handleFailure(new Error('composerStorage: budget trim write failed'))
        break // storage just failed; stop enforcing rather than spin
      }
      state.history = candidate.history // commit only after the write succeeded
      total = currentTotalBytes()
      if (total >= before) break // no progress made this pass — stop
    }
  }

  function maintainCaps() {
    if (!persistent) return
    enforceSessionCap()
    enforceByteBudget()
  }

  // Ordinary user-activity write path (debounced draft persistence,
  // clearDraft, markSending, recordSend, and re-enabling a preference):
  // always unions this write's history with whatever is currently on disk
  // via mergeWithPersisted, so a write from this tab can never silently
  // erase a send another tab already recorded for the same session. See the
  // top-of-file policy comment.
  function persistMerged(sessionName, state) {
    writeSessionRaw(sessionName, mergeWithPersisted(sessionName, state))
    maintainCaps()
  }

  // Explicit destructive/authoritative write path (clearHistory, pref
  // purge): writes this instance's state as-is, with no merge. Invariant:
  // only call this when the caller intends the write to override whatever is
  // currently on disk for the affected fields — e.g. "clear" must stay
  // cleared even if another tab wrote something in the interim. See the
  // top-of-file policy comment.
  function persistOverwrite(sessionName, state) {
    writeSessionRaw(sessionName, state)
    maintainCaps()
  }

  function persistSessionNow(sessionName) {
    const state = sessions.get(sessionName)
    if (!state) return
    // Debounced writes must merge with disk the same as every other mutator:
    // another tab may have recorded a send for this session while this
    // debounce was pending, and that history must survive this write.
    persistMerged(sessionName, state)
  }

  function schedulePersist(sessionName) {
    if (disposed) return
    cancelPendingTimer(sessionName)
    const id = scheduleTimeout(() => {
      pendingTimers.delete(sessionName)
      persistSessionNow(sessionName)
    }, debounceMs)
    pendingTimers.set(sessionName, id)
  }

  // Before persisting `sessionName`, re-read whatever is currently on disk
  // (which may have been written by another tab open on the same session
  // since this instance last loaded it) and merge history by stable identity
  // (text+at) rather than blindly overwriting. The writing instance's
  // draft/sending fields win (last writer wins for those), but history is a
  // union so a debounced draft write from one tab can never erase a send
  // recorded by another tab in the same session.
  function mergeWithPersisted(sessionName, state) {
    const raw = safeGet(sessionStorageKey(sessionName))
    if (!raw) return state
    let onDisk
    try {
      onDisk = sanitizeSessionState(JSON.parse(raw))
    } catch {
      return state
    }
    const seen = new Set(state.history.map(e => `${e.text}\u0000${e.at}`))
    const merged = [...state.history]
    for (const entry of onDisk.history) {
      const identity = `${entry.text}\u0000${entry.at}`
      if (!seen.has(identity)) {
        seen.add(identity)
        merged.push(entry)
      }
    }
    merged.sort((a, b) => b.at - a.at)
    return { ...state, history: merged.slice(0, HISTORY_MAX) }
  }

  function flushAll() {
    for (const pendingName of [...pendingTimers.keys()]) {
      cancelPendingTimer(pendingName)
      persistSessionNow(pendingName)
    }
  }

  // Explicit destructive purge (pref turned off): overwrite, not merge —
  // this instance's disabled preference must win even if another tab wrote
  // something to disk in the interim. See persistOverwrite policy comment.
  function purgeAllDrafts() {
    for (const name of allKnownNames()) {
      // Leave the in-memory draft alone (still usable for this page lifetime);
      // only the persisted representation drops it, via the prefs gate in
      // writeSessionRaw. This purge is authoritative for the draft field
      // only, so history must still union with what is on disk — another tab
      // may have recorded a send since this instance loaded the session.
      writeSessionRaw(name, mergeWithPersisted(name, loadSessionState(name)))
    }
    maintainCaps()
  }

  function purgeAllHistories() {
    for (const name of allKnownNames()) {
      const state = loadSessionState(name)
      state.history = []
      writeSessionRaw(name, state)
    }
    maintainCaps()
  }

  // Re-persist every session this instance currently knows about in memory.
  // Used when a preference flips back on so the in-memory draft/history that
  // accumulated while it was off lands on disk immediately, rather than
  // waiting for the next unrelated write (minor: re-enable persists now).
  // Ordinary (non-destructive) write, so it merges with disk like any other
  // user-activity write.
  function persistAllKnownSessions() {
    for (const [name, state] of sessions) persistMerged(name, state)
  }

  ensureIndexLoaded()

  // If a preference was already off before this instance existed (an older
  // build, another tab, or simply a reload after disabling it), purge any
  // stale persisted draft/history left on disk immediately rather than
  // waiting for the next unrelated write.
  {
    const initialPrefs = getPrefs()
    if (persistent && (!initialPrefs.draft || !initialPrefs.history)) {
      for (const name of listPersistedSessionNames()) {
        writeSessionRaw(name, loadSessionState(name)) // purge, per persistOverwrite policy
      }
      maintainCaps()
    }
  }

  return {
    isPersistent() {
      return persistent
    },

    getPrefs,

    setPrefs(partial) {
      if (disposed) return getPrefs()
      const current = getPrefs()
      const next = {
        draft: typeof partial?.draft === 'boolean' ? partial.draft : current.draft,
        history: typeof partial?.history === 'boolean' ? partial.history : current.history,
      }
      cachedPrefs = next
      if (persistent) {
        safeSet(PREFS_KEY, JSON.stringify(next))
      }
      if (current.draft && !next.draft) purgeAllDrafts()
      if (current.history && !next.history) purgeAllHistories()
      if (!current.draft && next.draft) persistAllKnownSessions()
      if (!current.history && next.history) persistAllKnownSessions()
      return { ...next }
    },

    getSession(sessionName) {
      const name = normalizeName(sessionName)
      if (!name) return emptySessionState()
      return cloneSessionState(loadSessionState(name))
    },

    setDraft(sessionName, text) {
      if (disposed) return
      const name = normalizeName(sessionName)
      if (!name) return
      const state = loadSessionState(name)
      state.draft = truncateUtf8(typeof text === 'string' ? text : '', DRAFT_MAX_BYTES)
      if (!getPrefs().draft) {
        // Memory-only for the rest of this page lifetime; never scheduled.
        cancelPendingTimer(name)
        return
      }
      schedulePersist(name)
    },

    // Immediate persistence for the successful-send path: drop the draft
    // right now rather than waiting for the debounce, so a send that clears
    // the box can't race a stale debounced write from before it.
    clearDraft(sessionName) {
      if (disposed) return
      const name = normalizeName(sessionName)
      if (!name) return
      cancelPendingTimer(name)
      const state = loadSessionState(name)
      state.draft = ''
      persistMerged(name, state)
    },

    flush(sessionName) {
      if (disposed) return
      const name = normalizeName(sessionName)
      if (name) {
        if (!pendingTimers.has(name)) return
        cancelPendingTimer(name)
        persistSessionNow(name)
        return
      }
      flushAll()
    },

    markSending(sessionName, sending) {
      if (disposed) return
      const name = normalizeName(sessionName)
      if (!name) return
      const state = loadSessionState(name)
      state.sending = !!sending
      persistMerged(name, state)
    },

    recordSend(sessionName, text, at = nowFn()) {
      if (disposed) return
      const name = normalizeName(sessionName)
      if (!name) return
      const state = loadSessionState(name)
      if (getPrefs().history) {
        const truncated = truncateUtf8(String(text ?? ''), ENTRY_MAX_BYTES)
        const timestamp = typeof at === 'number' ? at : nowFn()
        const top = state.history[0]
        if (top && top.text === truncated) {
          state.history = [{ text: truncated, at: timestamp }, ...state.history.slice(1)]
        } else {
          state.history = [{ text: truncated, at: timestamp }, ...state.history].slice(0, HISTORY_MAX)
        }
      }
      persistMerged(name, state)
    },

    // Explicit destructive action: authoritative overwrite, not a merge.
    // Clearing history must stay cleared even if another tab's send lands
    // on disk concurrently (see persistOverwrite policy comment).
    clearHistory(sessionName) {
      if (disposed) return
      const name = normalizeName(sessionName)
      if (!name) return
      const state = loadSessionState(name)
      state.history = []
      persistOverwrite(name, state)
    },

    pruneSessions(activeNames) {
      if (disposed) return
      const active = new Set(Array.isArray(activeNames) ? activeNames : [])
      for (const name of allKnownNames()) {
        if (!active.has(name)) forgetSession(name)
      }
    },

    dispose() {
      if (disposed) return
      flushAll()
      disposed = true
    },
  }
}
