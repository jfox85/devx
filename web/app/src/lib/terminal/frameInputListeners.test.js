import { describe, it, expect, vi, beforeEach } from 'vitest'
import { attachFrameInputListeners, _resetAttachedDocs } from './frameInputListeners.js'

// Build a fresh fake iframe document for each test. happy-dom gives us a real
// EventTarget with addEventListener/dispatchEvent so capture-phase wiring and
// event delivery behave like a browser document.
function makeDoc() {
  // A standalone HTML document standing in for an iframe's contentDocument.
  return document.implementation.createHTMLDocument('frame')
}

function makeHandlers() {
  return {
    onKeydown: vi.fn(),
    onPaste: vi.fn(),
    onDragEnter: vi.fn(),
    onDragLeave: vi.fn(),
    onDragOver: vi.fn((e) => e.preventDefault?.()),
    onDrop: vi.fn((e) => e.preventDefault?.()),
  }
}

describe('attachFrameInputListeners', () => {
  let doc
  beforeEach(() => {
    doc = makeDoc()
    _resetAttachedDocs(doc)
  })

  it('wires a paste listener that fires when the event is dispatched', () => {
    const handlers = makeHandlers()
    const attached = attachFrameInputListeners(doc, handlers)

    expect(attached).toBe(true)

    // Dispatch a paste like an image-from-clipboard would produce.
    doc.dispatchEvent(new Event('paste', { bubbles: true }))
    expect(handlers.onPaste).toHaveBeenCalledTimes(1)
  })

  // This is the core regression: previously, listeners were only registered
  // inside handleIframeLoad, which the on:load handler skipped whenever the
  // frame finished loading while a *different* session was active. The frame
  // then had no paste listener, and the warm pool-promotion path reused it
  // without re-attaching — so paste silently did nothing.
  //
  // Here we model that timeline: attach happens "while inactive" (the only
  // thing that matters is that attach is called on load regardless of active
  // state), and a later warm-promotion re-call must NOT double-wire but the
  // paste listener must still be live.
  it('keeps paste working after a warm-promotion re-attach (idempotent)', () => {
    const handlers = makeHandlers()

    // Frame loads while inactive: attach is called once.
    expect(attachFrameInputListeners(doc, handlers)).toBe(true)

    // User switches back: warm promotion calls attach again on the same doc.
    // It must be a no-op (already wired) rather than adding a second listener.
    expect(attachFrameInputListeners(doc, handlers)).toBe(false)

    doc.dispatchEvent(new Event('paste', { bubbles: true }))
    // Exactly once: live listener, and no duplicate from the second call.
    expect(handlers.onPaste).toHaveBeenCalledTimes(1)
  })

  it('wires keydown and drag/drop handlers too', () => {
    const handlers = makeHandlers()
    attachFrameInputListeners(doc, handlers)

    doc.dispatchEvent(new Event('keydown', { bubbles: true }))
    doc.dispatchEvent(new Event('dragenter', { bubbles: true }))
    doc.dispatchEvent(new Event('dragleave', { bubbles: true }))
    doc.dispatchEvent(new Event('dragover', { bubbles: true }))
    doc.dispatchEvent(new Event('drop', { bubbles: true }))

    expect(handlers.onKeydown).toHaveBeenCalledTimes(1)
    expect(handlers.onDragEnter).toHaveBeenCalledTimes(1)
    expect(handlers.onDragLeave).toHaveBeenCalledTimes(1)
    expect(handlers.onDragOver).toHaveBeenCalledTimes(1)
    expect(handlers.onDrop).toHaveBeenCalledTimes(1)
  })

  it('omits the paste listener when onPaste is not provided', () => {
    // The desktop/terminal-bridge path owns iframe image paste via an injected
    // ttyd script + postMessage, so Terminal.svelte attaches without onPaste.
    // The other listeners must still wire, and dispatching paste must not throw.
    const handlers = makeHandlers()
    delete handlers.onPaste

    expect(attachFrameInputListeners(doc, handlers)).toBe(true)
    expect(() => doc.dispatchEvent(new Event('paste', { bubbles: true }))).not.toThrow()

    doc.dispatchEvent(new Event('keydown', { bubbles: true }))
    expect(handlers.onKeydown).toHaveBeenCalledTimes(1)
  })

  it('is a safe no-op for a missing/cross-origin document', () => {
    const handlers = makeHandlers()
    expect(attachFrameInputListeners(null, handlers)).toBe(false)
    expect(attachFrameInputListeners(undefined, handlers)).toBe(false)
    expect(handlers.onPaste).not.toHaveBeenCalled()
  })

  it('wires independent documents separately (one frame does not satisfy another)', () => {
    const handlers = makeHandlers()
    const docA = makeDoc()
    const docB = makeDoc()
    _resetAttachedDocs(docA)
    _resetAttachedDocs(docB)

    expect(attachFrameInputListeners(docA, handlers)).toBe(true)
    // docB is a different frame and must be wired on its own.
    expect(attachFrameInputListeners(docB, handlers)).toBe(true)

    docB.dispatchEvent(new Event('paste', { bubbles: true }))
    expect(handlers.onPaste).toHaveBeenCalledTimes(1)
  })
})
