// Per-session UI state, kept for the app lifetime.
// Composer drafts and prompt history are persisted to localStorage via the
// composerStorage singleton below (see
// docs/plans/2026-09-05-composer-draft-persistence-and-history.md): a typed,
// unsent prompt survives a reload/tab-discard, scoped per session. Layout
// chrome (split mode, artifact pane) remains in-memory only: it restores
// across session switches within the tab, which is the case that matters,
// without persisting anything.
import { createComposerStorage, mergeRecall as mergeRecallPure } from '../composer/composerStorage.js'

const chrome = new Map()

const composerStorage = createComposerStorage()

export function getComposerDraft(sessionName) {
  return composerStorage.getSession(sessionName).draft
}

export function setComposerDraft(sessionName, value) {
  if (!sessionName) return
  composerStorage.setDraft(sessionName, value)
}

export function clearComposerDraft(sessionName) {
  if (!sessionName) return
  composerStorage.clearDraft(sessionName)
}

export function getComposerSession(sessionName) {
  return composerStorage.getSession(sessionName)
}

export function markComposerSending(sessionName, sending) {
  composerStorage.markSending(sessionName, sending)
}

export function recordComposerSend(sessionName, text, at) {
  composerStorage.recordSend(sessionName, text, at)
}

export function clearComposerHistory(sessionName) {
  composerStorage.clearHistory(sessionName)
}

// Client-only privacy preferences (devx_composer_prefs_v1): { draft, history },
// both default true. Exposed here so PromptHistorySheet — which must not
// import storage directly — can read/write them via props/callbacks wired
// through PromptComposer. Setting either to false purges its already-stored
// data via composerStorage's purge semantics (see composerStorage.js); it
// never touches text currently sitting in a textarea (memory-only draft
// state lives in the Svelte component, not here).
export function getComposerPrefs() {
  return composerStorage.getPrefs()
}

export function setComposerPrefs(partial) {
  return composerStorage.setPrefs(partial)
}

export function mergeComposerRecall(currentText, entryText) {
  return mergeRecallPure(currentText, entryText)
}

export function flushComposerDrafts(sessionName) {
  composerStorage.flush(sessionName)
}

export function pruneComposerSessions(names) {
  composerStorage.pruneSessions(names)
}

export function disposeComposerStorage() {
  composerStorage.dispose()
}

// Layout chrome: { splitMode, artifactPaneOpen, selectedArtifactID }
export function getSessionChrome(sessionName) {
  return chrome.get(sessionName) || null
}

export function setSessionChrome(sessionName, state) {
  if (!sessionName) return
  chrome.set(sessionName, { ...chrome.get(sessionName), ...state })
}

// --- Switch-latency instrumentation -----------------------------------------
// Lightweight timing around terminal switching: click → iframe load → xterm
// ready. Logged to the console (debug) and kept in a small ring buffer so we
// can compare cold vs warm switch times against the plan's budgets:
//   warm/prewarmed visible < 500ms, cold visible < 2500ms.
const MAX_SAMPLES = 50
const samples = []
let current = null

export function markSwitchStart(sessionName) {
  current = {
    session: sessionName,
    start: performance.now(),
    iframeLoad: null,
    ready: null,
    prewarmed: prewarmedSessions.has(sessionName),
  }
}

export function markIframeLoad(sessionName) {
  if (!current || current.session !== sessionName || current.iframeLoad !== null) return
  current.iframeLoad = performance.now() - current.start
}

export function markTerminalReady(sessionName) {
  if (!current || current.session !== sessionName || current.ready !== null) return
  current.ready = performance.now() - current.start
  samples.push(current)
  if (samples.length > MAX_SAMPLES) samples.shift()
  const s = current
  console.debug(
    `[devx] switch ${s.session}: iframe ${s.iframeLoad?.toFixed(0) ?? '?'}ms, ` +
    `ready ${s.ready.toFixed(0)}ms (${s.prewarmed ? 'warm' : 'cold'})`
  )
  current = null
}

export function getSwitchSamples() {
  return [...samples]
}

// Expose for manual inspection in devtools: window.__devxPerf()
if (typeof window !== 'undefined') {
  window.__devxPerf = () => {
    const warm = samples.filter(s => s.prewarmed && s.ready !== null)
    const cold = samples.filter(s => !s.prewarmed && s.ready !== null)
    const avg = arr => arr.length ? Math.round(arr.reduce((a, s) => a + s.ready, 0) / arr.length) : null
    return {
      samples: [...samples],
      warmAvgMs: avg(warm), warmCount: warm.length,
      coldAvgMs: avg(cold), coldCount: cold.length,
    }
  }
}

// --- Prewarm tracking --------------------------------------------------------
// Which sessions the client has successfully prewarmed (terminal ready before
// the user opened it). Used to label perf samples warm vs cold.
const prewarmedSessions = new Set()

export function markPrewarmed(sessionName, ready) {
  if (ready) prewarmedSessions.add(sessionName)
  else prewarmedSessions.delete(sessionName)
}
