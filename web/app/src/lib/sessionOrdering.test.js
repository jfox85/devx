import test from 'node:test'
import assert from 'node:assert/strict'
import {
  SESSION_VIEW_KEY,
  loadSessionView,
  saveSessionView,
  buildSessionSections,
  compareRecent,
} from './sessionOrdering.js'

const session = (name, activity, project, extra = {}) => ({
  name,
  activity_at: activity,
  project_alias: project,
  status: { priority: 5 },
  ...extra,
})

test('recent view is global with pinned sessions in one leading section', () => {
  const sessions = [
    session('older-a', '2026-08-18T10:00:00Z', 'alpha'),
    session('newer-b', '2026-08-18T12:00:00Z', 'beta'),
    session('pinned-a', '2026-08-18T09:00:00Z', 'alpha', { pinned: true }),
  ]
  const sections = buildSessionSections(sessions, '', 'recent')
  assert.deepEqual(sections.map(s => [s.key, s.sessions.map(x => x.name)]), [
    ['__pinned__', ['pinned-a']],
    ['__recent__', ['newer-b', 'older-a']],
  ])
})

test('projects view keeps unpinned project/status ordering and no duplicates', () => {
  const sessions = [
    session('low', '2026-08-18T12:00:00Z', 'beta', { status: { priority: 8 } }),
    session('urgent', '2026-08-18T10:00:00Z', 'beta', { status: { priority: 1 } }),
    session('pinned', '2026-08-18T09:00:00Z', 'alpha', { pinned: true }),
    session('loose', null, ''),
  ]
  const sections = buildSessionSections(sessions, '', 'projects')
  assert.deepEqual(sections.map(s => [s.key, s.sessions.map(x => x.name)]), [
    ['__pinned__', ['pinned']],
    ['project:beta', ['urgent', 'low']],
    ['__no_project__', ['loose']],
  ])
  assert.equal(sections.flatMap(s => s.sessions).length, sessions.length)
})

test('reserved section names cannot collide with project aliases', () => {
  const sections = buildSessionSections([
    session('favorite', '2026-08-18T12:00:00Z', 'alpha', { pinned: true }),
    session('project-row', '2026-08-18T11:00:00Z', 'pinned'),
  ], '', 'projects')
  assert.equal(new Set(sections.map(s => s.key)).size, sections.length)
  assert.deepEqual(sections.map(s => s.key), ['__pinned__', 'project:pinned'])

  const noProjectCollision = buildSessionSections([
    session('real-alias', '2026-08-18T12:00:00Z', '__no_project__'),
    session('loose', '2026-08-18T11:00:00Z', ''),
  ], '', 'projects')
  assert.deepEqual(noProjectCollision.map(s => s.key), ['project:__no_project__', '__no_project__'])
})

test('equal valid activity timestamps use deterministic name ties', () => {
  const values = [
    session('beta', '2026-08-18T12:00:00Z', ''),
    session('alpha', '2026-08-18T12:00:00Z', ''),
  ]
  assert.ok(compareRecent(values[0], values[1]) > 0)
  assert.ok(compareRecent(values[1], values[0]) < 0)
  assert.deepEqual(values.sort(compareRecent).map(s => s.name), ['alpha', 'beta'])
})

test('invalid activity sorts last with deterministic name ties', () => {
  const values = [session('zeta', null, ''), session('beta', 'bad', ''), session('alpha', '2026-08-18T12:00:00Z', '')]
  assert.deepEqual(values.sort(compareRecent).map(s => s.name), ['alpha', 'beta', 'zeta'])
})

test('view preference validates storage and survives inaccessible storage', () => {
  const values = new Map()
  const storage = {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
  }
  assert.equal(loadSessionView(storage), 'recent')
  saveSessionView(storage, 'projects')
  assert.equal(values.get(SESSION_VIEW_KEY), 'projects')
  assert.equal(loadSessionView(storage), 'projects')
  values.set(SESSION_VIEW_KEY, 'invalid')
  assert.equal(loadSessionView(storage), 'recent')
  assert.equal(loadSessionView({ getItem: () => { throw new Error('blocked') } }), 'recent')
})
