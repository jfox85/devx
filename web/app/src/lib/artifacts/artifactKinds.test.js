import { test } from 'node:test'
import assert from 'node:assert/strict'
import { renderKind, isViewable, downloadName, downloadURL, neighborIndex, artifactExt, indexOfID } from './artifactKinds.js'

test('indexOfID follows the same artifact when the list reloads', () => {
  const before = [{ id: 'a' }, { id: 'b' }, { id: 'c' }]
  assert.equal(indexOfID(before, 'b', 0), 1)
  // A new artifact sorts first: the viewer stays on 'b', now at index 2.
  const after = [{ id: 'new' }, ...before]
  assert.equal(indexOfID(after, 'b', 1), 2)
  // 'b' was removed: stay at the previous position, clamped to the list.
  assert.equal(indexOfID([{ id: 'a' }, { id: 'c' }], 'b', 1), 1)
  assert.equal(indexOfID([{ id: 'a' }], 'b', 5), 0)
  assert.equal(indexOfID([], 'b', 3), 0)
})

test('renderKind classifies by type and extension', () => {
  assert.equal(renderKind({ type: 'screenshot', file: 'screenshots/a.png' }), 'image')
  assert.equal(renderKind({ type: 'other', file: 'x/clip.MP4' }), 'video')
  assert.equal(renderKind({ type: 'document', file: 'mock.jsx' }), 'jsx')
  assert.equal(renderKind({ type: 'log', file: 'run.log' }), 'text')
  assert.equal(renderKind({ type: 'report', file: 'r.html' }), 'html')
  assert.equal(renderKind({ type: 'document', file: 'spec.pdf' }), 'iframe')
  assert.equal(renderKind({ type: 'other', file: 'blob.bin' }), 'other')
  assert.equal(renderKind(null), 'other')
})

test('artifactExt ignores dots in folders and dotfiles', () => {
  assert.equal(artifactExt({ file: 'v1.2/readme' }), '')
  assert.equal(artifactExt({ file: '.env' }), '')
  assert.equal(artifactExt({ file: 'a/b.PNG' }), 'png')
})

test('isViewable excludes jsx and unknown files', () => {
  assert.equal(isViewable({ file: 'a.png' }), true)
  assert.equal(isViewable({ file: 'a.jsx' }), false)
  assert.equal(isViewable({ file: 'a.zip' }), false)
})

test('downloadName uses the file basename', () => {
  assert.equal(downloadName({ file: 'screenshots/login.png' }), 'login.png')
  assert.equal(downloadName({}), 'artifact')
})

test('downloadURL appends download=1', () => {
  assert.equal(downloadURL({ url: '/sessions/s/artifacts/a.png' }), '/sessions/s/artifacts/a.png?download=1')
  assert.equal(downloadURL({ url: '/api/artifacts/file?id=1' }), '/api/artifacts/file?id=1&download=1')
  assert.equal(downloadURL({}), '')
})

test('neighborIndex skips non-viewable items and wraps', () => {
  const items = [{ file: 'a.png' }, { file: 'b.zip' }, { file: 'c.mp4' }]
  assert.equal(neighborIndex(items, 0, 1), 2)
  assert.equal(neighborIndex(items, 2, 1), 0)
  assert.equal(neighborIndex(items, 0, -1), 2)
  assert.equal(neighborIndex([{ file: 'a.png' }], 0, 1), -1)
  assert.equal(neighborIndex([], 0, 1), -1)
})
