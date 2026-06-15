// Per-session UI state, kept in memory for the app lifetime.
// Composer drafts are intentionally memory-only (never persisted) so prompt
// text doesn't outlive the tab. Layout chrome (split mode, artifact pane) is
// also in-memory: it restores across session switches, which is the case that
// matters, without persisting anything.
const drafts = new Map()
const chrome = new Map()

export function getComposerDraft(sessionName) {
  return drafts.get(sessionName) || ''
}

export function setComposerDraft(sessionName, value) {
  if (!sessionName) return
  if (value) drafts.set(sessionName, value)
  else drafts.delete(sessionName)
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
