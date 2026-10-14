// Open/download behaviour for artifacts, shared by the artifact pane and the
// fullscreen viewer. Each surface needs a different mechanism:
//   - Desktop (Wails): WKWebView ignores target=_blank and cannot download, so
//     the native host opens the file in the default app or shows a Save dialog.
//   - Browser / mobile PWA: open the artifact URL in a new tab, and download
//     through a ?download=1 URL that the server sends as an attachment, which
//     also works where the <a download> attribute is ignored (iOS Safari).
import { openArtifact as desktopOpen, downloadArtifact as desktopDownload } from '../desktopBridge.js'
import { downloadName, downloadURL } from './artifactKinds.js'

// openArtifactExternally resolves to a short status message, or '' when there
// is nothing to report. Must be called from a user gesture so the browser does
// not block the new tab.
export async function openArtifactExternally(sessionName, a) {
  if (!a) return ''
  const native = desktopOpen(sessionName, a.id)
  if (native) {
    await native
    return ''
  }
  window.open(a.url, '_blank', 'noopener,noreferrer')
  return ''
}

export async function downloadArtifactFile(sessionName, a) {
  if (!a) return ''
  const native = desktopDownload(sessionName, a.id)
  if (native) {
    const saved = await native
    return saved ? `Saved to ${saved}` : ''
  }
  const link = document.createElement('a')
  link.href = downloadURL(a)
  link.download = downloadName(a)
  link.rel = 'noopener'
  link.style.display = 'none'
  document.body.appendChild(link)
  link.click()
  link.remove()
  return ''
}
