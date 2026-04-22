// web/app/src/api.js
const base = '/api'

// handle401 clears the auth marker and reloads so the login form reappears.
function handle401() {
  localStorage.removeItem('devx_authed')
  window.location.reload()
}

// apiFetch sends requests relying on the httpOnly session cookie for auth.
// The bearer-token approach is intentionally omitted — storing the raw token
// in script-readable localStorage would expose it to XSS. The server's auth
// middleware checks the cookie that was set at login time.
async function apiFetch(path, options = {}) {
  const res = await fetch(base + path, {
    ...options,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...options.headers,
    },
  })
  if (res.status === 401) {
    handle401()
    throw new Error('Unauthorized')
  }
  return res
}

export async function listSessions() {
  const res = await apiFetch('/sessions')
  const data = await res.json()
  return data.sessions || []
}

export async function createSession(name, project) {
  const res = await apiFetch('/sessions', {
    method: 'POST',
    body: JSON.stringify({ name, project }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to create session')
  }
  return res.json()
}

export async function deleteSession(name) {
  const res = await apiFetch('/sessions?name=' + encodeURIComponent(name), { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to delete session: ${res.status}`)
}

export async function flagSession(name) {
  const res = await apiFetch('/sessions/flag?name=' + encodeURIComponent(name), { method: 'POST' })
  if (!res.ok) throw new Error(`Failed to flag session: ${res.status}`)
}

export async function unflagSession(name) {
  const res = await apiFetch('/sessions/flag?name=' + encodeURIComponent(name), { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to unflag session: ${res.status}`)
}

export async function login(token) {
  const res = await fetch(base + '/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
  if (!res.ok) {
    throw new Error('Invalid token')
  }
  // Store a non-sensitive marker so the app knows to show the session list.
  // The actual auth token lives only in the httpOnly cookie set by the server.
  localStorage.setItem('devx_authed', '1')
}

export async function listWindows(sessionName) {
  const res = await apiFetch('/windows?name=' + encodeURIComponent(sessionName))
  const data = await res.json()
  return data.windows || []
}

export async function switchWindow(sessionName, windowIndex) {
  await apiFetch(
    '/switch-window?name=' + encodeURIComponent(sessionName) + '&window=' + windowIndex,
    { method: 'POST' }
  )
}

export async function listProjects() {
  const res = await apiFetch('/projects')
  const data = await res.json()
  return data.projects || []
}

export async function refreshTerminal(sessionName) {
  await apiFetch('/refresh?name=' + encodeURIComponent(sessionName), { method: 'POST' })
}

export async function sendKeys(sessionName, keys) {
  await apiFetch(
    '/send-keys?name=' + encodeURIComponent(sessionName) + '&keys=' + encodeURIComponent(keys),
    { method: 'POST' }
  )
}

// sendLiteral injects text verbatim into the active pane — spaces are preserved.
// Use this for file paths; use sendKeys for named tmux key sequences (C-b, Enter, etc.).
export async function sendLiteral(sessionName, text) {
  await apiFetch(
    '/send-keys?mode=literal&name=' + encodeURIComponent(sessionName) + '&keys=' + encodeURIComponent(text),
    { method: 'POST' }
  )
}

export async function uploadImage(file) {
  const form = new FormData()
  form.append('image', file)
  const res = await fetch(base + '/upload-image', {
    method: 'POST',
    credentials: 'same-origin',
    body: form,
  })
  if (res.status === 401) {
    handle401()
    throw new Error('Unauthorized')
  }
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Upload failed')
  }
  return res.json()  // { path: '/Users/.../.devx/uploads/abc123.png' }
}

export function isLoggedIn() {
  return !!localStorage.getItem('devx_authed')
}

// subscribeToEvents opens an SSE connection to /api/events and registers
// handlers for each named event type in the handlers map.
// Example: subscribeToEvents({ show: fn, flag: fn })
// Returns a cleanup function that closes the connection.
export function subscribeToEvents(handlers) {
  const es = new EventSource('/api/events', { withCredentials: true })
  for (const [event, fn] of Object.entries(handlers)) {
    es.addEventListener(event, (e) => {
      try {
        fn(JSON.parse(e.data))
      } catch { /* ignore malformed events */ }
    })
  }
  return () => es.close()
}
