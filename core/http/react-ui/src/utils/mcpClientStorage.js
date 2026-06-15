import { generateId } from './format'

const STORAGE_KEY = 'localai_client_mcp_servers'

// localStorage is shared across same-origin pages; an XSS that lands once can
// poison persisted MCP server entries to attempt header injection or to feed
// a non-http URL into the fetch path. Validate every entry on load and drop
// anything that doesn't match the expected shape.
function sanitiseServer(s) {
  if (!s || typeof s !== 'object') return null
  const id = typeof s.id === 'string' ? s.id : ''
  const name = typeof s.name === 'string' ? s.name : ''
  const url = typeof s.url === 'string' ? s.url : ''
  if (!url) return null
  // fetch() refuses non-http schemes anyway, but reject early so they
  // can't get persisted back out. URL parsing also catches malformed values.
  let parsed
  try {
    parsed = new URL(url)
  } catch {
    return null
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null

  const headers = {}
  if (s.headers && typeof s.headers === 'object' && !Array.isArray(s.headers)) {
    for (const [k, v] of Object.entries(s.headers)) {
      // Drop CRLF / control chars to block header injection through poisoned storage.
      if (typeof k !== 'string' || typeof v !== 'string') continue
      if (/[\x00-\x1f\x7f]/.test(k) || /[\x00-\x1f\x7f]/.test(v)) continue
      headers[k] = v
    }
  }
  return {
    id,
    name,
    url,
    headers,
    useProxy: s.useProxy !== false,
  }
}

export function loadClientMCPServers() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      const data = JSON.parse(stored)
      if (Array.isArray(data)) {
        return data.map(sanitiseServer).filter(Boolean)
      }
    }
  } catch (_e) {
    // ignore
  }
  return []
}

export function saveClientMCPServers(servers) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(servers))
  } catch (_e) {
    // ignore
  }
}

export function addClientMCPServer({ name, url, headers, useProxy }) {
  const servers = loadClientMCPServers()
  const server = {
    id: generateId(),
    name: name || new URL(url).hostname,
    url,
    headers: headers || {},
    useProxy: useProxy !== false,
  }
  servers.push(server)
  saveClientMCPServers(servers)
  return server
}

export function removeClientMCPServer(id) {
  const servers = loadClientMCPServers().filter(s => s.id !== id)
  saveClientMCPServers(servers)
  return servers
}

export function updateClientMCPServer(id, updates) {
  const servers = loadClientMCPServers().map(s =>
    s.id === id ? { ...s, ...updates } : s
  )
  saveClientMCPServers(servers)
  return servers
}
