// Notification helpers for flag events.

// requestNotificationPermission asks the browser for notification permission.
// Should be called once after login. Silently ignores errors (e.g. the browser
// may not support notifications, or the user may dismiss the prompt).
export function requestNotificationPermission() {
  if (typeof Notification === 'undefined') return
  if (Notification.permission === 'default') {
    Notification.requestPermission().catch(() => {})
  }
}

// notifyFlag handles a flag SSE event using focus-aware routing:
// - Window focused  → show in-app toast (toast is visible, native would be suppressed)
// - Window not focused → show native notification (gets OS-level attention)
// - Not focused + no permission → fall back to toast
//
// onNavigate(sessionName) — called when the user clicks the notification
// showFallback(event)     — called to show the in-app toast
export function notifyFlag(event, { onNavigate, showFallback }) {
  const { session, reason } = event
  const windowFocused = document.hasFocus()
  const title = `DevX: ${session}`
  const body = reason ? String(reason) : 'Session needs attention'
  const host = typeof window !== 'undefined' && window.go?.main?.Host

  if (!windowFocused && host?.Notify) {
    // Desktop shell: use native OS notifications. Keep the session name in the
    // title and the caller-supplied reason in the body so the alert is actionable.
    host.Notify(title, body).catch(() => showFallback(event))
    return
  }

  if (!windowFocused && typeof Notification !== 'undefined' && Notification.permission === 'granted') {
    // Background browser/PWA tab: use browser notification so it gets through.
    const n = new Notification(title, {
      body,
      tag: `devx-flag-${session}`, // replaces any earlier notification for the same session
    })
    n.onclick = () => {
      window.focus()
      onNavigate(session)
      n.close()
    }
  } else {
    // Foreground tab/window (or no permission): show in-app toast.
    showFallback(event)
  }
}
