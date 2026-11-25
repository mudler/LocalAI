// Address helpers of the launcher config, shared by the desktop main process
// and a client-only shell. Pure, no Electron or Node import.

/**
 * A saved address is host:port. An older or hand-edited config may hold a URL
 * ("http://localhost:8080/"); the server rejects that with "too many colons",
 * so the scheme and any path are dropped. Anything else is returned as is.
 */
export function normalizeAddress(address: string): string {
  const m = /^[a-z][a-z0-9+.-]*:\/\/([^/?#]*)/i.exec(address.trim())
  if (!m) return address
  const hp = m[1].replace(/^[^@]*@/, '')
  if (hp === '') return '127.0.0.1:8080'
  const i = hp.lastIndexOf(':')
  return i < 0 || hp.endsWith(']') ? `${hp}:8080` : hp
}

export function isLoopbackHost(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase()
  if (h === 'localhost' || h === '::1') return true
  return /^127(\.\d{1,3}){3}$/.test(h)
}
