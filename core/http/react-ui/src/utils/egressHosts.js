// The "Always allowed hosts" list of Settings > Tools (chats_egress_allowed_hosts).
// The setting is a comma separated string of exact host names, each with an
// optional ":port". The server checks the same rules again and has the last
// word (core/config/chats_egress.go); the checks here only tell the person
// what is wrong before they apply.

export const MAX_ALLOWED_HOSTS = 64

const LABEL = /^[a-z0-9_]([a-z0-9_-]*[a-z0-9_])?$/

// The entries of the setting value, in order, empty ones dropped.
export function hostEntries(value) {
  return String(value || '').split(',').map(s => s.trim()).filter(Boolean)
}

// The value of the setting for a list of entries.
export function hostsValue(entries) {
  return entries.join(', ')
}

function privateV4(parts) {
  const [a, b] = parts
  return a === 0 || a === 10 || a === 127 || (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || a >= 224
}

// Reads one typed entry. Returns { entry } with the canonical form (lower case,
// no trailing dot, punycode, ":port" only when given), or { error } with a key
// of settings.hosts.errors in the admin locale.
export function checkHost(raw, { existing = [], allowPrivate = false } = {}) {
  let text = String(raw || '').trim()
  if (!text) return { error: 'empty' }
  if (text.includes('*')) return { error: 'wildcard' }
  if (text.includes('://') || /[/?#@\\\s]/.test(text)) return { error: 'shape' }
  let port = ''
  let host = text
  const colons = (text.match(/:/g) || []).length
  if (text.startsWith('[')) {
    const m = text.match(/^\[([^\]]+)\](?::(\d+))?$/)
    if (!m) return { error: 'shape' }
    host = m[1]
    port = m[2] || ''
  } else if (colons === 1) {
    ;[host, port] = text.split(':')
    if (!/^\d+$/.test(port)) return { error: 'port' }
  }
  if (port) {
    const n = Number(port)
    if (n < 1 || n > 65535 || n === 22 || n === 25) return { error: 'port' }
  }
  host = host.toLowerCase().replace(/\.$/, '')
  if (host.includes(':')) {
    // An IPv6 literal.
    if (!allowPrivate || !/^[0-9a-f:.]+$/.test(host)) return { error: 'private' }
  } else if (/^[0-9.]+$/.test(host)) {
    const parts = host.split('.')
    if (parts.length !== 4 || parts.some(p => p === '' || p.length > 3 || Number(p) > 255)) return { error: 'numeric' }
    if (!allowPrivate && privateV4(parts.map(Number))) return { error: 'private' }
  } else {
    if (host === 'localhost' || host.endsWith('.localhost')) {
      if (!allowPrivate) return { error: 'private' }
    }
    let ascii = host
    try {
      ascii = new URL(`http://${host}`).hostname
    } catch {
      return { error: 'shape' }
    }
    if (!ascii || ascii.length > 253 || !ascii.split('.').every(l => l.length <= 63 && LABEL.test(l))) return { error: 'shape' }
    if (/^[0-9x.]+$/.test(ascii)) return { error: 'numeric' }
    host = ascii
  }
  const entry = port ? (host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`) : (host.includes(':') ? `[${host}]` : host)
  if (existing.includes(entry)) return { error: 'duplicate', entry }
  if (existing.length >= MAX_ALLOWED_HOSTS) return { error: 'full' }
  return { entry }
}
