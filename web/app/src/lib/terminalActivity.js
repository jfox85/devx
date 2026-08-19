export function createTerminalAttempt(cryptoImpl = globalThis.crypto) {
  if (cryptoImpl?.randomUUID) return cryptoImpl.randomUUID()
  if (cryptoImpl?.getRandomValues) {
    const bytes = cryptoImpl.getRandomValues(new Uint8Array(16))
    return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  }
  throw new Error('secure random support is required')
}

export function terminalFramePath(name, attempt) {
  const path = `/terminal/${encodeURIComponent(name)}/`
  const params = new URLSearchParams({ devx_attempt: attempt })
  return `${path}?${params}`
}
