// Pure helpers shared by the artifact pane and the fullscreen viewer. Kept free
// of DOM/Svelte so they can be unit-tested with node --test.

const IMAGE_EXTS = ['png', 'jpg', 'jpeg', 'gif', 'webp']
const VIDEO_EXTS = ['webm', 'mp4', 'mov']
const TEXT_EXTS = ['md', 'txt', 'log', 'diff', 'patch', 'js', 'ts', 'css', 'json']

export function artifactExt(a) {
  const file = a?.file || ''
  const base = file.split('/').pop() || ''
  const dot = base.lastIndexOf('.')
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : ''
}

// renderKind classifies how an artifact can be previewed inline.
export function renderKind(a) {
  if (!a) return 'other'
  const ext = artifactExt(a)
  if (a.type === 'screenshot' || IMAGE_EXTS.includes(ext)) return 'image'
  if (a.type === 'recording' || VIDEO_EXTS.includes(ext)) return 'video'
  if (['jsx', 'tsx'].includes(ext)) return 'jsx'
  if (['log', 'diff'].includes(a.type) || TEXT_EXTS.includes(ext)) return 'text'
  if (['html', 'htm'].includes(ext) || ['plan', 'report'].includes(a.type)) return 'html'
  if (ext === 'pdf' || a.type === 'document') return 'iframe'
  return 'other'
}

// Kinds the fullscreen viewer can render on its own (no JSX runtime needed).
const VIEWABLE = ['image', 'video', 'html', 'iframe', 'text']

export function isViewable(a) {
  return VIEWABLE.includes(renderKind(a))
}

// downloadName is the filename offered when saving an artifact.
export function downloadName(a) {
  const base = (a?.file || '').split('/').pop()
  return base || 'artifact'
}

// downloadURL asks the server to send the file as an attachment, which makes
// downloads work even where the <a download> attribute is ignored (iOS Safari,
// some installed PWAs).
export function downloadURL(a) {
  const url = a?.url || ''
  if (!url) return ''
  return url + (url.includes('?') ? '&' : '?') + 'download=1'
}

// neighborIndex steps through viewable artifacts, wrapping at either end.
// Returns -1 when no other viewable artifact exists.
export function neighborIndex(items, index, step) {
  const n = items?.length || 0
  if (n === 0) return -1
  for (let i = 1; i < n; i++) {
    const j = (((index + step * i) % n) + n) % n
    if (isViewable(items[j])) return j
  }
  return -1
}
