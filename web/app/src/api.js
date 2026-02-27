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
  const res = await apiFetch(`/sessions/${name}`, { method: 'DELETE' })
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

export function isLoggedIn() {
  return !!localStorage.getItem('devx_token')
}
