// Single adapter for the Wails desktop shell. All access to the native bridge —
// the injected window.__DEVX_DESKTOP config, the window.go.main.Host bindings,
// and the devx:desktop:* DOM event names the host dispatches — goes through
// here so the rest of the SPA never reaches into globals directly. Every helper
// is a no-op (or returns null) in a plain browser, so callers don't need their
// own "are we in the desktop app?" guards.

// Event names the native host dispatches as window CustomEvents. Kept in lockstep
// with the Go dispatcher in desktop/main.go (dispatchDroppedFiles).
export const DESKTOP_EVENTS = {
  fileDrop: 'devx:desktop:filedrop',
  fileDropRejected: 'devx:desktop:filedrop-rejected',
}

// isDesktop reports whether the SPA is running inside the Wails shell.
export function isDesktop() {
  return typeof window !== 'undefined' && !!window.__DEVX_DESKTOP
}

// desktopConfig returns the injected { terminalBase, terminalToken } config, or
// null in a browser. The terminal iframe uses it to connect directly to the
// private loopback origin.
export function desktopConfig() {
  return (typeof window !== 'undefined' && window.__DEVX_DESKTOP) || null
}

function host() {
  return (typeof window !== 'undefined' && window.go?.main?.Host) || null
}

// clipboardImage asks the native host for the current clipboard image as base64,
// or null if there is no host binding or no image. WKWebView often omits
// clipboard images from the DOM paste event, so the desktop paste path calls
// this instead.
export async function clipboardImage() {
  const binding = host()?.ClipboardImage
  if (typeof binding !== 'function') return null
  const data = await binding()
  return data || null
}

// openExternal opens a URL in the user's default browser via the host. Returns
// true if it handled the open (desktop), false in a browser so the caller can
// fall back to default link behaviour. onError runs if the native call rejects.
export function openExternal(url, onError) {
  const binding = host()?.OpenExternal
  if (typeof binding !== 'function') return false
  binding(url).catch((err) => onError?.(err))
  return true
}

// notify shows a native OS notification via the host. Returns true if handled
// (desktop), false in a browser so the caller can fall back to a Web/in-app
// notification. onError runs if the native call rejects.
export function notify(title, body, onError) {
  const binding = host()?.Notify
  if (typeof binding !== 'function') return false
  binding(title, body).catch(() => onError?.())
  return true
}
