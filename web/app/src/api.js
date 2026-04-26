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

export async function renameSession(name, displayName) {
  const params = new URLSearchParams({ name })
  if (displayName != null) params.set('display_name', displayName)
  const res = await apiFetch('/sessions/rename?' + params.toString(), { method: 'POST' })
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    throw new Error(err.error || 'Rename failed')
  }
}

export async function colorSession(name, color) {
  const res = await apiFetch(
    '/sessions/color?name=' + encodeURIComponent(name) + '&color=' + encodeURIComponent(color),
    { method: 'POST' }
  )
  if (!res.ok) {
    const err = await res.json().catch(() => ({}))
    throw new Error(err.error || 'Color change failed')
  }
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

export async function getActivePane(sessionName) {
  const res = await apiFetch('/active-pane?name=' + encodeURIComponent(sessionName))
  const data = await res.json()
  return data.pane
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

export async function getSettings() {
  const res = await apiFetch('/settings')
  if (!res.ok) return { artifact_trigger_key: 'Ctrl+Space' }
  return res.json()
}

export async function refreshTerminal(sessionName) {
  await apiFetch('/refresh?name=' + encodeURIComponent(sessionName), { method: 'POST' })
}

async function requireOK(res, fallbackMessage) {
  if (res.ok) return
  const e = await res.json().catch(() => ({}))
  throw new Error(e.error || fallbackMessage || `Request failed: ${res.status}`)
}

export async function sendKeys(sessionName, keys) {
  const res = await apiFetch(
    '/send-keys?name=' + encodeURIComponent(sessionName) + '&keys=' + encodeURIComponent(keys),
    { method: 'POST' }
  )
  await requireOK(res, 'Failed to send keys')
}

// sendLiteral injects text verbatim into the active pane — spaces are preserved.
// Use this for file paths; use sendKeys for named tmux key sequences (C-b, Enter, etc.).
export async function sendLiteral(sessionName, text) {
  const res = await apiFetch(
    '/send-keys?mode=literal&name=' + encodeURIComponent(sessionName) + '&keys=' + encodeURIComponent(text),
    { method: 'POST' }
  )
  await requireOK(res, 'Failed to send text')
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

export async function listArtifacts(sessionName, filters = {}) {
  const params = new URLSearchParams({ session: sessionName })
  for (const [key, value] of Object.entries(filters)) {
    if (value) params.set(key, value)
  }
  const res = await apiFetch('/artifacts?' + params.toString())
  if (!res.ok) throw new Error(`Failed to list artifacts: ${res.status}`)
  const data = await res.json()
  return data.artifacts || []
}

export async function uploadArtifacts(sessionName, files, { title = '', tags = '', retention = 'session', type = '', summary = '' } = {}) {
  const form = new FormData()
  for (const file of files || []) form.append('file', file)
  if (title) form.append('title', title)
  if (tags) form.append('tags', tags)
  if (retention) form.append('retention', retention)
  if (type) form.append('type', type)
  if (summary) form.append('summary', summary)
  const res = await fetch(base + '/artifacts/upload?session=' + encodeURIComponent(sessionName), {
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
    throw new Error(e.error || 'Artifact upload failed')
  }
  const data = await res.json()
  return data.artifacts || []
}

export async function createTextArtifact(sessionName, { title, text, format = 'md', tags = '', retention = 'session', type = 'document' }) {
  const form = new FormData()
  form.append('title', title)
  form.append('text', text)
  form.append('format', format)
  form.append('type', type)
  if (tags) form.append('tags', tags)
  if (retention) form.append('retention', retention)
  const res = await fetch(base + '/artifacts/upload?session=' + encodeURIComponent(sessionName), {
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
    throw new Error(e.error || 'Artifact creation failed')
  }
  const data = await res.json()
  return data.artifacts || []
}

export async function archiveArtifact(sessionName, id) {
  const res = await apiFetch('/artifacts/archive?session=' + encodeURIComponent(sessionName) + '&id=' + encodeURIComponent(id), { method: 'POST' })
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Artifact archive failed')
  }
  return res.json()
}

export async function removeArtifact(sessionName, id) {
  const res = await apiFetch('/artifacts/item?session=' + encodeURIComponent(sessionName) + '&id=' + encodeURIComponent(id), { method: 'DELETE' })
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Artifact remove failed')
  }
}

export async function clearArtifactFocus(sessionName) {
  const res = await apiFetch('/artifacts/focus?session=' + encodeURIComponent(sessionName), { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to clear artifact focus: ${res.status}`)
}

export async function renameArtifact(sessionName, id, patch) {
  const res = await apiFetch('/artifacts/rename?session=' + encodeURIComponent(sessionName) + '&id=' + encodeURIComponent(id), {
    method: 'POST',
    body: JSON.stringify(patch),
  })
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Artifact rename failed')
  }
  return res.json()
}

export async function getShareIntent(id) {
  const res = await apiFetch('/share-intents/' + encodeURIComponent(id))
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Shared content not found')
  }
  return res.json()
}

export async function commitShareIntent(id, payload) {
  const res = await apiFetch('/share-intents/' + encodeURIComponent(id), {
    method: 'POST',
    body: JSON.stringify(payload),
  })
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Failed to create artifact from shared content')
  }
  const data = await res.json()
  return data.artifacts || []
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
