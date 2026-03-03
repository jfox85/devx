// web/app/src/api.js
const base = '/api'

function getToken() {
  return localStorage.getItem('devx_token') || ''
}

async function apiFetch(path, options = {}) {
  const res = await fetch(base + path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer ' + getToken(),
      ...options.headers,
    },
  })
  if (res.status === 401) {
    localStorage.removeItem('devx_token')
    window.location.reload()
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
  const res = await apiFetch(`/sessions/${name}/flag`, { method: 'POST' })
  if (!res.ok) throw new Error(`Failed to flag session: ${res.status}`)
}

export async function unflagSession(name) {
  const res = await apiFetch(`/sessions/${name}/flag`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`Failed to unflag session: ${res.status}`)
}

export async function login(token) {
  const res = await fetch(base + '/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  })
  if (!res.ok) {
    throw new Error('Invalid token')
  }
  localStorage.setItem('devx_token', token)
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

export async function uploadImage(file) {
  const form = new FormData()
  form.append('image', file)
  const res = await fetch(base + '/upload-image', {
    method: 'POST',
    headers: { 'Authorization': 'Bearer ' + getToken() },
    body: form,
  })
  if (res.status === 401) {
    localStorage.removeItem('devx_token')
    window.location.reload()
    throw new Error('Unauthorized')
  }
  if (!res.ok) {
    const e = await res.json().catch(() => ({}))
    throw new Error(e.error || 'Upload failed')
  }
  return res.json()  // { path: '/Users/.../.devx/uploads/abc123.png' }
}

export function isLoggedIn() {
  return !!localStorage.getItem('devx_token')
}
