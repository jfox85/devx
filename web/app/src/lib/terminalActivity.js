export function createTerminalAttempt(cryptoImpl = globalThis.crypto) {
  if (!cryptoImpl?.randomUUID) throw new Error('secure random UUID support is required')
  return cryptoImpl.randomUUID()
}

export function terminalFramePath(name, attempt) {
  const path = `/terminal/${encodeURIComponent(name)}/`
  const params = new URLSearchParams({ devx_attempt: attempt })
  return `${path}?${params}`
}
