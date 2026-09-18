import { describe, it, expect } from 'vitest'

// The desktop keyboard proxy relays keystrokes over HTTP through a sequential
// promise queue. Two properties of that queue caused (or nearly caused) silent
// input loss, and neither is observable from a DOM test, so they are pinned
// here against a faithful reimplementation of the production shape in
// Terminal.svelte's enqueueKeyboardProxyInput.
function createQueue() {
  const state = { queue: Promise.resolve(), failures: 0, generation: 0, reported: [] }

  state.enqueue = (send) => {
    const generation = state.generation
    state.queue = state.queue
      .then(() => send())
      .then(() => {
        if (generation === state.generation) state.failures = 0
      })
      .catch(error => {
        if (generation === state.generation) {
          state.failures += 1
          state.reported.push(error.message)
        }
      })
    return state.queue
  }

  // Mirrors the session-switch reset in Terminal.svelte.
  state.switchSession = () => {
    state.queue = Promise.resolve()
    state.failures = 0
    state.generation += 1
  }

  return state
}

describe('keyboard proxy queue', () => {
  it('reports a failed relay instead of swallowing it', async () => {
    const q = createQueue()
    await q.enqueue(() => Promise.reject(new Error('rate limit exceeded')))
    expect(q.reported).toEqual(['rate limit exceeded'])
  })

  it('keeps running after a failure so later keystrokes are still delivered', async () => {
    const q = createQueue()
    const delivered = []

    await q.enqueue(() => Promise.reject(new Error('boom')))
    await q.enqueue(() => { delivered.push('second'); return Promise.resolve() })

    // A rejected queue would never run the second send.
    expect(delivered).toEqual(['second'])
  })

  it('resets the failure count once a relay succeeds', async () => {
    const q = createQueue()
    await q.enqueue(() => Promise.reject(new Error('boom')))
    expect(q.failures).toBe(1)

    await q.enqueue(() => Promise.resolve())
    expect(q.failures).toBe(0)
  })

  it('counts consecutive failures so the toast text changes and re-arms its timer', async () => {
    const q = createQueue()
    await q.enqueue(() => Promise.reject(new Error('a')))
    await q.enqueue(() => Promise.reject(new Error('b')))
    expect(q.failures).toBe(2)
  })

  // Reassigning the queue on a session switch does NOT cancel a request already
  // in flight; the old chain's callbacks still run. Without the generation
  // guard, that late reply would report an error for a session the user has
  // already left, or clear the new session's failure count.
  it('ignores a late failure from a previous session', async () => {
    const q = createQueue()
    let rejectInFlight
    const inFlight = new Promise((_, reject) => { rejectInFlight = reject })

    q.enqueue(() => inFlight)
    q.switchSession()
    rejectInFlight(new Error('stale session error'))
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(q.reported).toEqual([])
  })

  it('ignores a late success from a previous session', async () => {
    const q = createQueue()
    let resolveInFlight
    const inFlight = new Promise(resolve => { resolveInFlight = resolve })

    q.enqueue(() => inFlight)
    q.switchSession()
    // The new session has its own failure, which a stale success must not clear.
    await q.enqueue(() => Promise.reject(new Error('new session failure')))
    resolveInFlight()
    await new Promise(resolve => setTimeout(resolve, 0))

    expect(q.failures).toBe(1)
  })
})
