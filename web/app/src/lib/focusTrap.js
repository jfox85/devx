// Modal Tab handling that never defers to the browser's native tab order.
//
// WebKit (Safari, and the desktop app's WKWebView) only tabs between text
// fields and selects by default — buttons and radios are skipped. A trap that
// only intervenes on the first/last control therefore never fires there, and
// focus escapes the overlay into whatever is behind it (e.g. the terminal).
// Instead, own every Tab press inside the dialog and move focus explicitly.

const FOCUSABLE = 'input, select, textarea, button, a[href], [tabindex]:not([tabindex="-1"])'

export function tabbableControls(container) {
  const all = Array.from(container?.querySelectorAll(FOCUSABLE) || [])
    .filter(el => !el.disabled && el.tabIndex !== -1 && el.offsetParent !== null)
  // A radio group is a single tab stop: keep the checked radio, or the first
  // one when none is checked. Arrow keys move within the group natively.
  return all.filter(el => {
    if (el.type !== 'radio' || !el.name) return true
    const group = all.filter(o => o.type === 'radio' && o.name === el.name)
    const checked = group.find(o => o.checked)
    return el === (checked || group[0])
  })
}

// Call from a dialog's keydown handler. Returns true when the event was a Tab
// that has been handled (default prevented, focus moved within container).
export function trapTab(e, container) {
  if (e.key !== 'Tab' || e.ctrlKey || e.metaKey || e.altKey) return false
  const controls = tabbableControls(container)
  e.preventDefault()
  if (controls.length === 0) {
    container?.focus?.()
    return true
  }
  const active = document.activeElement
  let index = controls.indexOf(active)
  // Focus sits on a non-stop member of a radio group: treat it as the group.
  if (index === -1 && active?.type === 'radio' && active.name) {
    index = controls.findIndex(el => el.type === 'radio' && el.name === active.name)
  }
  let next
  if (index === -1) next = e.shiftKey ? controls[controls.length - 1] : controls[0]
  else next = controls[(index + (e.shiftKey ? -1 : 1) + controls.length) % controls.length]
  next.focus()
  return true
}
