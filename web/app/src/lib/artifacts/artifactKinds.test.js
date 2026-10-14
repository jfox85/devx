import { test } from 'node:test'
import assert from 'node:assert/strict'
import { renderKind, isViewable, downloadName, downloadURL, neighborIndex, artifactExt } from './artifactKinds.js'

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
