// Per-frame input listener attachment for the terminal iframe keep-alive pool.
//
// The pool keeps several session iframes mounted at once, but only one
// Terminal.svelte instance drives them. Paste/keydown/drag/drop listeners must
// be attached to each iframe's contentDocument exactly once, and crucially they
// must be attached even when the iframe finishes loading while a *different*
// session is active — otherwise that frame has no paste/drag support when it is
// later promoted to the foreground (warm pool hit, which does not re-fire the
// iframe `load` event). See the regression test for the exact failure mode.
//
// Extracted from Terminal.svelte so the attach/dedup invariant can be unit
// tested without mounting the full terminal component.

// Tracks documents that already have listeners, so repeated calls (load +
// warm promotion) are no-ops. WeakSet so evicted frames are GC'd.
const attachedDocs = new WeakSet()

// Attach input listeners to a frame's contentDocument exactly once.
//
// handlers: {
//   onKeydown(e), onPaste(e),   // forwarded with capture: true
//   onDragEnter(e), onDragLeave(e), onDragOver(e), onDrop(e),
// }
//
// Returns true if listeners were newly attached, false if this document was
// already wired (or unavailable). Safe to call with a null/cross-origin doc.
export function attachFrameInputListeners(doc, handlers) {
  if (!doc || attachedDocs.has(doc)) return false
  try {
    doc.addEventListener('keydown', handlers.onKeydown, { capture: true })
    doc.addEventListener('paste', handlers.onPaste, { capture: true })
    // Drag events do not bubble across iframe boundaries, so a file dragged
    // over the iframe never reaches the outer div's dragenter/drop handlers.
    // Mirror the events onto the parent window so the drop overlay appears and
    // the file is processed correctly.
    doc.addEventListener('dragenter', handlers.onDragEnter)
    doc.addEventListener('dragleave', handlers.onDragLeave)
    doc.addEventListener('dragover', handlers.onDragOver)
    doc.addEventListener('drop', handlers.onDrop)
    attachedDocs.add(doc)
    return true
  } catch {
    // contentDocument not accessible yet (cross-origin / not loaded) — ignore.
    return false
  }
}

// Test-only: forget that a document was wired. Not used in production.
export function _resetAttachedDocs(doc) {
  if (doc) attachedDocs.delete(doc)
}
