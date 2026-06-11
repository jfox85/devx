const drafts = new Map()

export function getComposerDraft(sessionName) {
  return drafts.get(sessionName) || ''
}

export function setComposerDraft(sessionName, value) {
  if (!sessionName) return
  if (value) drafts.set(sessionName, value)
  else drafts.delete(sessionName)
}
