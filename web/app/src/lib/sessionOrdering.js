export const SESSION_VIEW_KEY = 'devx_session_list_view_v1'
export const SESSION_VIEWS = new Set(['recent', 'projects'])

export function loadSessionView(storage = globalThis.localStorage) {
  try {
    const value = storage?.getItem(SESSION_VIEW_KEY)
    return SESSION_VIEWS.has(value) ? value : 'recent'
  } catch {
    return 'recent'
  }
}

export function saveSessionView(storage = globalThis.localStorage, value) {
  if (!SESSION_VIEWS.has(value)) return
  try { storage?.setItem(SESSION_VIEW_KEY, value) } catch { /* browser storage may be disabled */ }
}

export function activityMillis(value) {
  if (typeof value !== 'string' || value.trim() === '') return null
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : null
}

export function compareRecent(a, b) {
  const left = activityMillis(a.activity_at)
  const right = activityMillis(b.activity_at)
  if (left !== null && right !== null && left !== right) return right - left
  if (left !== null) return -1
  if (right !== null) return 1
  return a.name.localeCompare(b.name)
}

export function comparePinnedRecent(a, b) {
  return Number(!!b.pinned) - Number(!!a.pinned) || compareRecent(a, b)
}

export function compareProjectStatus(a, b) {
  return (a.status?.priority ?? 99) - (b.status?.priority ?? 99) || a.name.localeCompare(b.name)
}

export function filterSessions(sessions, query) {
  const needle = query.trim().toLowerCase()
  if (!needle) return sessions
  return sessions.filter(s => [s.name, s.display_name, s.project_alias, s.branch]
    .filter(Boolean)
    .some(value => value.toLowerCase().includes(needle)))
}

export function buildSessionSections(sessions, query = '', view = 'recent') {
  const filtered = filterSessions(sessions, query)
  const pinned = filtered.filter(s => s.pinned).sort(compareRecent)
  const unpinned = filtered.filter(s => !s.pinned)
  const sections = []
  if (pinned.length) sections.push({ key: '__pinned__', kind: 'pinned', label: 'Pinned', showProject: true, showActivity: true, sessions: pinned })

  if (view === 'projects') {
    const groups = new Map()
    for (const item of unpinned) {
      const key = item.project_alias ? `project:${encodeURIComponent(item.project_alias)}` : '__no_project__'
      if (!groups.has(key)) groups.set(key, [])
      groups.get(key).push(item)
    }
    const keys = [...groups.keys()].sort((a, b) => {
      if (a === '__no_project__') return 1
      if (b === '__no_project__') return -1
      return a.localeCompare(b)
    })
    for (const key of keys) {
      sections.push({
        key,
        kind: 'project',
        label: key === '__no_project__' ? 'No project' : decodeURIComponent(key.slice('project:'.length)),
        showProject: false,
        showActivity: false,
        sessions: groups.get(key).sort(compareProjectStatus),
      })
    }
  } else if (unpinned.length) {
    sections.push({ key: '__recent__', kind: 'recent', label: 'Recent', showProject: true, showActivity: true, sessions: unpinned.sort(compareRecent) })
  }
  return sections
}

export function relativeActivity(session, now = Date.now()) {
  const at = activityMillis(session.activity_at)
  if (at === null) return { short: 'never', label: 'Never opened' }
  const seconds = Math.max(0, Math.floor((now - at) / 1000))
  let short = 'now'
  if (seconds >= 86400) short = `${Math.floor(seconds / 86400)}d`
  else if (seconds >= 3600) short = `${Math.floor(seconds / 3600)}h`
  else if (seconds >= 60) short = `${Math.floor(seconds / 60)}m`
  const prefix = session.last_opened_at ? 'Opened' : 'Created'
  return { short, label: `${prefix} ${short === 'now' ? 'now' : `${short} ago`}` }
}
